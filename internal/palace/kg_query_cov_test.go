package palace

import (
	"context"
	"errors"
	"regexp"
	"sync/atomic"
	"testing"
)

// countdownCtx reports nil from Err for the first n calls and Canceled after,
// so a query is canceled at a chosen point of its evaluation.
type countdownCtx struct {
	context.Context
	left atomic.Int64
}

func newCountdown(n int) *countdownCtx {
	c := &countdownCtx{Context: context.Background()}
	c.left.Store(int64(n))
	return c
}

func (c *countdownCtx) Err() error {
	if c.left.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

// TestQueryCancelAtEveryCheckpoint cancels each query after n context checks
// for every n up to completion: the outcome is always either the full result
// or context.Canceled, never a partial result, and every query is eventually
// canceled mid-flight at least once.
func TestQueryCancelAtEveryCheckpoint(t *testing.T) {
	f := newQfix(t)
	queries := []struct{ lang, q string }{
		{"sparql", `SELECT ?a ?b WHERE { ?a <knows> ?b . FILTER(?a != ?b) }`},
		{"sparql", `SELECT ?a ?n WHERE { ?a <age> ?x . OPTIONAL { ?a <name> ?n } }`},
		{"sparql", `SELECT ?a WHERE { { ?a <knows> ?b } UNION { ?a <age> ?b } }`},
		{"sparql", `SELECT ?a WHERE { ?a <age> ?x . FILTER NOT EXISTS { ?a <knows> ?b } }`},
		{"sparql", `SELECT ?s WHERE { GRAPH <g> { ?s <knows> ?o } }`},
		{"sparql", `SELECT (COUNT(*) AS ?n) WHERE { ?a <knows> ?b }`},
		{"cypher", `MATCH (a) OPTIONAL MATCH (a)-[:knows]->(b) RETURN a, b.name`},
		{"cypher", `MATCH (a)-[:knows]->(b) WHERE NOT a.name = 'x' RETURN a, b`},
	}
	for _, c := range queries {
		t.Run(c.lang+"/"+c.q, func(t *testing.T) {
			var full *QueryResult
			canceled := 0
			for n := 0; n < 200; n++ {
				res, err := f.kg.Now().RunQuery(newCountdown(n), c.lang, c.q, QueryOptions{})
				switch {
				case err == nil:
					full = res
				case errors.Is(err, context.Canceled):
					if res != nil {
						t.Fatalf("n=%d: partial result with error", n)
					}
					canceled++
					continue
				default:
					t.Fatalf("n=%d: unexpected error %v", n, err)
				}
				break
			}
			if full == nil {
				t.Fatal("query never completed")
			}
			if canceled < 2 {
				t.Fatalf("only %d cancellation points", canceled)
			}
		})
	}
}

func TestQueryIRDirect(t *testing.T) {
	f := newQfix(t)
	ctx := context.Background()
	sel := func(where ...QElem) (*QueryResult, error) {
		q := &Query{Vars: []QProj{{Name: "a", Var: "a"}}, Where: where}
		return f.kg.Now().Select(ctx, q, QueryOptions{})
	}
	knows := QTriple{S: QVar("a"), P: QConst(Ent("knows")), O: QVar("b")}

	// A QNode on an already-bound entity keeps the solution; on a bound
	// literal it drops it.
	res, err := sel(knows, QNode{Var: "a"})
	if err != nil {
		t.Fatal(err)
	}
	wantCol(t, res, "a", "alice", "alice", "bob", "dave")
	res, err = sel(QTriple{S: QVar("s"), P: QConst(Ent("name")), O: QVar("a")}, QNode{Var: "a"})
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("literal-bound node: %v %v", res, err)
	}
	// A QNode over everything enumerates entities in first-appearance order.
	res, err = sel(QNode{Var: "a"})
	if err != nil {
		t.Fatal(err)
	}
	wantCol(t, res, "a", "alice", "bob", "carol", "dave")
	// A QNode reads every statement, so the predicate set is unknown.
	if res.Preds != nil {
		t.Fatalf("preds = %v", res.Preds)
	}
	// QNode on an empty view is no rows, not a panic.
	empty, _ := newEngine(t)
	res, err = empty.Now().Select(ctx, &Query{Vars: []QProj{{Name: "a", Var: "a"}}, Where: []QElem{QNode{Var: "a"}}}, QueryOptions{})
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("empty view: %v %v", res, err)
	}
	// A node scan is charged per binding.
	_, err = f.kg.Now().Select(ctx, &Query{Vars: []QProj{{Name: "a", Var: "a"}}, Where: []QElem{QNode{Var: "a"}}},
		QueryOptions{Budget: Budget{MaxRows: 10}})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("node budget: %v", err)
	}
	// An unknown expression operator is unknown, so the row is rejected.
	res, err = sel(knows, QFilter{Expr: &QExpr{Op: "frobnicate"}})
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("unknown op: %v %v", res, err)
	}
	// A bound statement variable that is not a statement matches nothing.
	res, err = sel(
		QTriple{S: QVar("a"), P: QConst(Ent("knows")), O: QVar("b")},
		QTriple{S: QVar("x"), P: QConst(Ent("knows")), O: QVar("y"), EID: "b"},
	)
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("non-statement eid: %v %v", res, err)
	}
	// A bound statement variable restricts the match to that statement.
	res, err = sel(
		QTriple{S: QVar("a"), P: QConst(Ent("knows")), O: QVar("b"), EID: "r"},
		QTriple{S: QVar("x"), P: QVar("p"), O: QVar("y"), EID: "r"},
	)
	if err != nil || len(res.Rows) != 4 {
		t.Fatalf("bound eid: %v %v", res, err)
	}
	// A variable repeated in one pattern must agree.
	res, err = sel(QTriple{S: QVar("a"), P: QConst(Ent("knows")), O: QVar("a")})
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("self loop: %v %v", res, err)
	}
	// A non-entity bound predicate matches nothing.
	res, err = sel(
		QTriple{S: QVar("s"), P: QConst(Ent("name")), O: QVar("n")},
		QTriple{S: QVar("a"), P: QVar("n"), O: QVar("y")},
	)
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("literal predicate: %v %v", res, err)
	}
	// An Aux accessor with an unbound subject matches nothing (and does not
	// fail), so an OPTIONAL around it leaves the variable unbound.
	res, err = sel(
		QOptional{Aux: true, Group: []QElem{QTriple{S: QVar("zz"), P: QConst(Ent("age")), O: QVar("a"), Aux: true}}},
	)
	if err != nil || len(res.Rows) != 1 || len(res.Rows[0]) != 0 {
		t.Fatalf("aux unbound: %v %v", res, err)
	}
	// Object classes filter statements by object kind.
	res, err = sel(QTriple{S: QConst(Ent("alice")), P: QVar("p"), O: QVar("a"), Class: ObjLiteral})
	if err != nil || len(res.Rows) != 2 {
		t.Fatalf("literal class: %v %v", res, err)
	}
	// Aux triples and QOptional.Aux do not make the query negative; a plain
	// OPTIONAL does.
	_, neg := analyze(&Query{Where: []QElem{QOptional{Aux: true}}})
	if neg {
		t.Fatal("aux optional is not negative")
	}
	_, neg = analyze(&Query{Where: []QElem{QOptional{}}})
	if !neg {
		t.Fatal("optional is negative")
	}
}

func TestQueryExprHelpers(t *testing.T) {
	var nilExpr *QExpr
	if nilExpr.nonMonotonic() {
		t.Fatal("nil expression is monotonic")
	}
	v := &QExpr{Op: "var", Var: "x"}
	c := &QExpr{Op: "const", Val: Int(1)}
	for _, tc := range []struct {
		name string
		e    *QExpr
		want bool
	}{
		{"cmp", &QExpr{Op: "<", Args: []*QExpr{v, c}}, false},
		{"not", &QExpr{Op: "!", Args: []*QExpr{v}}, true},
		{"bound", &QExpr{Op: "bound", Args: []*QExpr{v}}, true},
		{"nested", &QExpr{Op: "&&", Args: []*QExpr{c, {Op: "||", Args: []*QExpr{v, {Op: "!", Args: []*QExpr{c}}}}}}, true},
	} {
		if got := tc.e.nonMonotonic(); got != tc.want {
			t.Errorf("%s: nonMonotonic = %v", tc.name, got)
		}
	}

	// <= over every comparable kind.
	vars := map[string]Value{"a": Str("a"), "b": Str("b"), "i": Int(2), "n": Num(2)}
	le := func(l, r string) tri {
		e := &QExpr{Op: "<=", Args: []*QExpr{{Op: "var", Var: l}, {Op: "var", Var: r}}}
		return toTri(e.eval(vars))
	}
	if le("a", "b") != triTrue || le("b", "a") != triFalse || le("i", "n") != triTrue || le("a", "i") != triUnknown {
		t.Fatal("<= semantics")
	}
	// A regex expression with a non-string subject is unknown; contains with a
	// missing second operand is unknown.
	re := &QExpr{Op: "regex", Args: []*QExpr{{Op: "var", Var: "i"}}, Re: regexp.MustCompile("2")}
	if toTri(re.eval(vars)) != triUnknown {
		t.Fatal("regex on int")
	}
	ct := &QExpr{Op: "contains", Args: []*QExpr{{Op: "var", Var: "a"}, {Op: "var", Var: "missing"}}}
	if toTri(ct.eval(vars)) != triUnknown {
		t.Fatal("contains with unbound operand")
	}
	st := &QExpr{Op: "strstarts", Args: []*QExpr{{Op: "var", Var: "a"}, {Op: "var", Var: "i"}}}
	if toTri(st.eval(vars)) != triUnknown {
		t.Fatal("strstarts with int operand")
	}
}

func TestCompareAndOrderValues(t *testing.T) {
	now := TimeVal(*day("2024-01-01"))
	later := TimeVal(*day("2024-01-02"))
	cases := []struct {
		name string
		a, b Value
		cmp  int
		ok   bool
	}{
		{"int int", Int(1), Int(2), -1, true},
		{"int num", Int(2), Num(1.5), 1, true},
		{"num int", Num(2), Int(2), 0, true},
		{"num string", Num(2), Str("2"), 0, false},
		{"string string", Str("b"), Str("a"), 1, true},
		{"entity entity", Ent("a"), Ent("b"), -1, true},
		{"entity string", Ent("a"), Str("a"), 0, false},
		{"bool bool", Bool(false), Bool(true), -1, true},
		{"time time", later, now, 1, true},
		{"stmt stmt", StmtRef(3), StmtRef(3), 0, true},
		{"tx tx", txVal(1), txVal(2), -1, true},
		{"time bool", now, Bool(true), 0, false},
		{"invalid kinds", Value{Kind: 99}, Value{Kind: 99}, 0, false},
	}
	for _, c := range cases {
		got, ok := compareValues(c.a, c.b)
		if got != c.cmp || ok != c.ok {
			t.Errorf("%s: compareValues = %d,%v want %d,%v", c.name, got, ok, c.cmp, c.ok)
		}
	}

	// ORDER BY: unbound first, then numbers, strings, then by kind.
	order := []struct {
		name   string
		a      Value
		aok    bool
		b      Value
		bok    bool
		expect int
	}{
		{"both unbound", Value{}, false, Value{}, false, 0},
		{"a unbound", Value{}, false, Int(1), true, -1},
		{"b unbound", Int(1), true, Value{}, false, 1},
		{"number before string", Num(9), true, Str("a"), true, -1},
		{"string after number", Str("a"), true, Int(9), true, 1},
		{"string before entity", Str("z"), true, Ent("a"), true, -1},
		{"entity before bool", Ent("z"), true, Bool(false), true, -1},
		{"same rank int num", Int(3), true, Num(2.5), true, 1},
		{"same kind", Str("a"), true, Str("b"), true, -1},
	}
	for _, c := range order {
		if got := orderCmp(c.a, c.aok, c.b, c.bok); got != c.expect {
			t.Errorf("%s: orderCmp = %d want %d", c.name, got, c.expect)
		}
	}
	if kindRank(Int(1)) != kindRank(Num(1)) || kindRank(Str("")) != 1 {
		t.Fatal("kindRank")
	}
	if kindRank(Ent("a")) == kindRank(Bool(true)) {
		t.Fatal("distinct kinds share a rank")
	}
}

func TestQueryOrderByMixedKindsEndToEnd(t *testing.T) {
	f := newQfix(t)
	assert1(t, f.kg, Ent("zed"), Ent("mix"), Str("s"), Valid{})
	assert1(t, f.kg, Ent("zed"), Ent("mix"), Int(5), Valid{})
	assert1(t, f.kg, Ent("zed"), Ent("mix"), Bool(true), Valid{})
	assert1(t, f.kg, Ent("zed"), Ent("mix"), Ent("ent"), Valid{})
	assert1(t, f.kg, Ent("zed"), Ent("mix"), Num(1.5), Valid{})
	res := f.run(t, "sparql", `SELECT ?o WHERE { <zed> <mix> ?o } ORDER BY ?o`)
	wantCol(t, res, "o", "1.5", "5", "s", "ent", "true")
	res = f.run(t, "sparql", `SELECT ?o WHERE { <zed> <mix> ?o } ORDER BY DESC(?o)`)
	wantCol(t, res, "o", "true", "ent", "s", "5", "1.5")
	// An order key that names nothing is a no-op: both sides unbound.
	res = f.run(t, "sparql", `SELECT ?o WHERE { <zed> <mix> ?o } ORDER BY ?nothing`)
	if len(res.Rows) != 5 {
		t.Fatalf("rows = %d", len(res.Rows))
	}
}

func TestQueryDeferredFilterOrdering(t *testing.T) {
	f := newQfix(t)
	// A FILTER written before the pattern it needs still applies; NOT EXISTS
	// inside OPTIONAL and nested groups.
	res := f.run(t, "sparql", `SELECT ?p ?q WHERE { ?p <age> ?n . OPTIONAL { ?p <knows> ?q . FILTER NOT EXISTS { ?q <knows> ?z } } }`)
	wantCol(t, res, "p", "alice", "bob", "carol")
	wantCol(t, res, "q", "carol", "carol", "-")
	// OPTIONAL with several matches keeps every one and the outer solution
	// only when there is none.
	res = f.run(t, "sparql", `SELECT ?p ?q WHERE { ?p <age> ?n . OPTIONAL { ?p <knows> ?q } }`)
	wantCol(t, res, "p", "alice", "alice", "bob", "carol")
	wantCol(t, res, "q", "bob", "carol", "carol", "-")
	// UNION branches evaluate against the same incoming solution.
	res = f.run(t, "sparql", `SELECT ?p ?q WHERE { ?p <age> 25 . { ?p <knows> ?q } UNION { ?q <knows> ?p } }`)
	wantSorted(t, res, "q", "alice", "carol")
}

func covRunBudget(f *qfix, lang, q string, b Budget) (*QueryResult, error) {
	return f.kg.Now().RunQuery(context.Background(), lang, q, QueryOptions{Budget: b})
}

func isBudget(err error) bool { return errors.Is(err, ErrBudget) }
