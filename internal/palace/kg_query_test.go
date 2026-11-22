package palace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// qfix is the shared graph of the query tests.
type qfix struct {
	kg                   *KnowledgeGraph
	ab, ac, bc, da       int64 // knows edges
	ageA, ageB, ageC     int64
	nameA, nameB, confAB int64
	tConf                int64 // transaction that wrote the confidence layer
}

func newQfix(t *testing.T) *qfix {
	t.Helper()
	kg, _ := newEngine(t)
	f := &qfix{kg: kg}
	f.ab = assert1(t, kg, Ent("alice"), Ent("knows"), Ent("bob"), Valid{})
	f.ac = assert1(t, kg, Ent("alice"), Ent("knows"), Ent("carol"), Valid{})
	f.bc = assert1(t, kg, Ent("bob"), Ent("knows"), Ent("carol"), Valid{})
	f.da = assert1(t, kg, Ent("dave"), Ent("knows"), Ent("alice"), Valid{})
	f.ageA = assert1(t, kg, Ent("alice"), Ent("age"), Int(30), Valid{})
	f.ageB = assert1(t, kg, Ent("bob"), Ent("age"), Int(25), Valid{})
	f.ageC = assert1(t, kg, Ent("carol"), Ent("age"), Int(41), Valid{})
	f.nameA = assert1(t, kg, Ent("alice"), Ent("name"), Str("Alice"), Valid{})
	f.nameB = assert1(t, kg, Ent("bob"), Ent("name"), Str("Bob"), Valid{})
	rep := tx(t, kg, func(w *KGTx) error {
		var err error
		f.confAB, _, err = w.Annotate(f.ab, Ent("confidence"), Num(0.9))
		return err
	})
	f.tConf = rep.T
	return f
}

func (f *qfix) run(t *testing.T, lang, text string) *QueryResult {
	t.Helper()
	res, err := f.kg.Now().RunQuery(context.Background(), lang, text, QueryOptions{})
	if err != nil {
		t.Fatalf("%s %q: %v", lang, text, err)
	}
	return res
}

// col returns column c of every row as text, "-" for an unbound cell.
func col(res *QueryResult, c string) []string {
	var out []string
	for _, r := range res.Rows {
		if v, ok := r[c]; ok {
			out = append(out, v.String())
		} else {
			out = append(out, "-")
		}
	}
	return out
}

func wantCol(t *testing.T, res *QueryResult, c string, want ...string) {
	t.Helper()
	if got := col(res, c); !slices.Equal(got, want) {
		t.Fatalf("column %s = %v, want %v", c, got, want)
	}
}

func wantSorted(t *testing.T, res *QueryResult, c string, want ...string) {
	t.Helper()
	got := col(res, c)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("column %s = %v, want %v", c, got, want)
	}
}

func TestQueryTruthTable(t *testing.T) {
	T, F, U := triTrue, triFalse, triUnknown
	v := func(n string) *QExpr { return &QExpr{Op: "var", Var: n} }
	cst := func(x Value) *QExpr { return &QExpr{Op: "const", Val: x} }
	vars := map[string]Value{"t": Bool(true), "f": Bool(false), "n": Int(5)}
	// "u" is unbound: unknown.
	for _, c := range []struct {
		op   string
		a, b string
		want tri
	}{
		{"&&", "t", "t", T}, {"&&", "t", "f", F}, {"&&", "t", "u", U}, {"&&", "f", "u", F}, {"&&", "u", "f", F}, {"&&", "u", "u", U},
		{"||", "t", "u", T}, {"||", "u", "t", T}, {"||", "f", "u", U}, {"||", "f", "f", F}, {"||", "u", "u", U},
	} {
		got := toTri((&QExpr{Op: c.op, Args: []*QExpr{v(c.a), v(c.b)}}).eval(vars))
		if got != c.want {
			t.Errorf("%s %s %s = %d, want %d", c.a, c.op, c.b, got, c.want)
		}
	}
	for _, c := range []struct {
		a    string
		want tri
	}{{"t", F}, {"f", T}, {"u", U}} {
		if got := toTri((&QExpr{Op: "!", Args: []*QExpr{v(c.a)}}).eval(vars)); got != c.want {
			t.Errorf("!%s = %d, want %d", c.a, got, c.want)
		}
	}
	// A comparison with an unbound side, or across kinds, is unknown; its
	// negation too. bound() is never unknown.
	gt := &QExpr{Op: ">", Args: []*QExpr{v("u"), cst(Int(30))}}
	if toTri(gt.eval(vars)) != U || toTri((&QExpr{Op: "!", Args: []*QExpr{gt}}).eval(vars)) != U {
		t.Error("unknown comparison or its negation is not unknown")
	}
	if toTri((&QExpr{Op: "<", Args: []*QExpr{cst(Str("a")), cst(Int(1))}}).eval(vars)) != U {
		t.Error("ill-typed ordering is not unknown")
	}
	if toTri((&QExpr{Op: "bound", Args: []*QExpr{v("u")}}).eval(vars)) != F {
		t.Error("bound(unbound) != false")
	}
	if toTri((&QExpr{Op: "<", Args: []*QExpr{cst(Int(2)), cst(Num(2.5))}}).eval(vars)) != T {
		t.Error("int < number")
	}
}

func TestQueryFilterBoundaryRejectsUnknown(t *testing.T) {
	f := newQfix(t)
	// dave has no age: !(?age > 30) is unknown for him, so his row is rejected,
	// while bob (25) passes and alice (30) does not.
	res := f.run(t, "sparql", `PREFIX ex: <http://ex/>
SELECT ?p WHERE { ?p ex:knows ?q . OPTIONAL { ?p ex:age ?age } FILTER(!(?age > 30)) }`)
	wantSorted(t, res, "p", "bob", "alice", "alice")
	// And the explicit test for unbound keeps him.
	res = f.run(t, "sparql", `PREFIX ex: <http://ex/>
SELECT DISTINCT ?p WHERE { ?p ex:knows ?q . OPTIONAL { ?p ex:age ?age } FILTER(!bound(?age) || ?age < 26) }`)
	wantSorted(t, res, "p", "dave", "bob")
}

func TestQueryASTAndAsOf(t *testing.T) {
	f := newQfix(t)
	ctx := context.Background()
	q, err := ParseSPARQL(`PREFIX ex: <http://ex/> SELECT ?o WHERE { ex:alice ex:knows ?o }`)
	if err != nil {
		t.Fatal(err)
	}
	now, err := f.kg.Now().Select(ctx, q, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantSorted(t, now, "o", "bob", "carol")
	// Retract alice->carol in a later transaction.
	rep := tx(t, f.kg, func(w *KGTx) error { _, err := w.Retract(f.ac); return err })
	now, _ = f.kg.Now().Select(ctx, q, QueryOptions{})
	wantSorted(t, now, "o", "bob")
	old, err := f.kg.AsOf(rep.T-1).Select(ctx, q, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantSorted(t, old, "o", "bob", "carol")
	if !slices.Equal(old.Deps, []int64{f.ab, f.ac}) {
		t.Fatalf("as-of deps = %v", old.Deps)
	}
	// Before anything existed.
	if empty, _ := f.kg.AsOf(0).Select(ctx, q, QueryOptions{}); len(empty.Rows) != 0 {
		t.Fatalf("AsOf(0) = %v", empty.Rows)
	}
	// Engine predicates are never matched by user patterns.
	res, err := f.kg.Now().RunQuery(ctx, "sparql", `SELECT * WHERE { ?s ?p ?o }`, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res.Rows {
		if strings.HasPrefix(r["p"].Text, "sys:") {
			t.Fatalf("engine predicate leaked: %v", r)
		}
	}
	if res.Preds != nil {
		t.Fatalf("variable predicate must give nil Preds, got %v", res.Preds)
	}
}

func TestQueryDialectDifferences(t *testing.T) {
	kg, _ := newEngine(t)
	// Two statements with equal (s,p,o) and disjoint valid intervals.
	e1 := assert1(t, kg, Ent("alice"), Ent("knows"), Ent("bob"), Valid{From: day("2020-01-01"), To: day("2021-01-01")})
	e2 := assert1(t, kg, Ent("alice"), Ent("knows"), Ent("bob"), Valid{From: day("2022-01-01"), To: day("2023-01-01")})
	if e1 == e2 {
		t.Fatal("fixture: expected two statements")
	}
	ctx := context.Background()
	sp, err := kg.Now().RunQuery(ctx, "sparql", `SELECT ?b WHERE { <alice> <knows> ?b }`, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cy, err := kg.Now().RunQuery(ctx, "cypher", `MATCH (a {}) -[:knows]-> (b) RETURN b`, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.Rows) != 1 {
		t.Fatalf("SPARQL is a set over (s,p,o): %d rows", len(sp.Rows))
	}
	if !slices.Equal(sp.Deps, []int64{e1, e2}) {
		t.Fatalf("SPARQL deps must still cover both statements: %v", sp.Deps)
	}
	if len(cy.Rows) != 2 {
		t.Fatalf("Cypher is a bag over eids: %d rows", len(cy.Rows))
	}
	// Relationship isomorphism: with one knows statement, two relationship
	// patterns in one MATCH cannot both bind it; SPARQL joins freely.
	kg2, _ := newEngine(t)
	assert1(t, kg2, Ent("alice"), Ent("knows"), Ent("bob"), Valid{})
	sp, _ = kg2.Now().RunQuery(ctx, "sparql", `SELECT * WHERE { ?a <knows> ?b . ?c <knows> ?d }`, QueryOptions{})
	cy, _ = kg2.Now().RunQuery(ctx, "cypher", `MATCH (a)-[:knows]->(b), (c)-[:knows]->(d) RETURN a`, QueryOptions{})
	if len(sp.Rows) != 1 || len(cy.Rows) != 0 {
		t.Fatalf("isomorphism: sparql %d rows (want 1), cypher %d rows (want 0)", len(sp.Rows), len(cy.Rows))
	}
	// A separate OPTIONAL MATCH is its own scope.
	cy, _ = kg2.Now().RunQuery(ctx, "cypher", `MATCH (a)-[:knows]->(b) OPTIONAL MATCH (c)-[:knows]->(d) RETURN a, c`, QueryOptions{})
	if len(cy.Rows) != 1 || cy.Rows[0]["c"].String() != "alice" {
		t.Fatalf("optional match scope: %v", cy.Rows)
	}
}

func TestQueryBudgets(t *testing.T) {
	f := newQfix(t)
	ctx := context.Background()
	v := f.kg.Now()
	run := func(b Budget, text string) (*QueryResult, error) {
		return v.RunQuery(ctx, "sparql", text, QueryOptions{Budget: b})
	}
	const sel = `SELECT ?a ?b WHERE { ?a <knows> ?b }`
	if _, err := run(Budget{MaxRows: 3}, sel); !errors.Is(err, ErrBudget) {
		t.Fatalf("row budget: %v (4 statements read must fail, not truncate)", err)
	}
	full, err := run(Budget{}, sel)
	if err != nil || len(full.Rows) != 4 {
		t.Fatalf("no budget: %v %d", err, len(full.Rows))
	}
	same, err := run(Budget{MaxRows: 1000, MaxBytes: 1 << 20}, sel)
	if err != nil || len(same.Rows) != len(full.Rows) || !slices.Equal(same.Deps, full.Deps) {
		t.Fatalf("a roomy budget changed the result: %v", err)
	}

	// Bytes: an oversized literal.
	assert1(t, f.kg, Ent("alice"), Ent("bio"), Str(strings.Repeat("x", 200)), Valid{})
	if _, err := run(Budget{MaxBytes: 100}, `SELECT ?b WHERE { <alice> <bio> ?b }`); !errors.Is(err, ErrBudget) {
		t.Fatalf("bytes budget: %v", err)
	}

	// One budget across sibling lookups: each alone fits, together they do not.
	one := `SELECT (COUNT(*) AS ?n) WHERE { ?a <knows> ?b }`
	two := `SELECT (COUNT(*) AS ?n) WHERE { { ?a <knows> ?b } UNION { ?a <knows> ?b } }`
	b := Budget{MaxRows: 6} // 4 statements + 1 row = 5 alone; 8 + 1 together
	if _, err := run(b, one); err != nil {
		t.Fatalf("single lookup: %v", err)
	}
	if _, err := run(b, two); !errors.Is(err, ErrBudget) {
		t.Fatalf("siblings together: %v", err)
	}

	// Cancellation inside the join: the meter's context is canceled while the
	// snapshot's is live, so only the loops' own check can stop it.
	q, _ := ParseSPARQL(`SELECT * WHERE { ?a <knows> ?b . ?b <knows> ?c }`)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	err = f.kg.read(ctx, func(sq querier) error {
		_, err := v.selectQ(ctx, sq, q, newMeter(cctx, Budget{}))
		return err
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled join: %v", err)
	}
	if _, err := v.Select(cctx, q, QueryOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Select with a canceled ctx: %v", err)
	}
}

func TestQueryUnknownLanguageAndNil(t *testing.T) {
	f := newQfix(t)
	if _, err := f.kg.Now().RunQuery(context.Background(), "sql", "x", QueryOptions{}); !errors.Is(err, ErrQueryParse) {
		t.Fatalf("unknown language: %v", err)
	}
	if _, err := f.kg.Now().Select(context.Background(), nil, QueryOptions{}); !errors.Is(err, ErrQueryParse) {
		t.Fatalf("nil query: %v", err)
	}
}

func TestQuerySparqlCypherAgree(t *testing.T) {
	f := newQfix(t)
	for _, c := range []struct {
		name, sparql, cypher string
		col                  string
	}{
		{"edge", `SELECT ?b WHERE { <alice> <knows> ?b }`, `MATCH (a {})-[:knows]->(b) RETURN b`, "b"},
		{"two hops", `SELECT ?c WHERE { <alice> <knows> ?b . ?b <knows> ?c }`,
			`MATCH (a {name: 'Alice'})-[:knows]->(b)-[:knows]->(c) RETURN c`, "c"},
		{"property", `SELECT ?p WHERE { ?p <age> 30 }`, `MATCH (p {age: 30}) RETURN p`, "p"},
		{"filter", `SELECT ?p WHERE { ?p <age> ?n . FILTER(?n > 26) }`, `MATCH (p) WHERE p.age > 26 RETURN p`, "p"},
		{"count", `SELECT (COUNT(*) AS ?n) WHERE { ?a <knows> ?b }`, `MATCH (a)-[:knows]->(b) RETURN count(*) AS n`, "n"},
		{"order", `SELECT ?p ?n WHERE { ?p <age> ?n } ORDER BY DESC(?n) LIMIT 2`,
			`MATCH (p) RETURN p, p.age AS n ORDER BY n DESC LIMIT 2`, "p"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := f.run(t, "sparql", c.sparql)
			cy := f.run(t, "cypher", strings.Replace(c.cypher, "(a {})", "(a)", 1))
			if c.name == "edge" { // pin the subject in Cypher
				cy = f.run(t, "cypher", `MATCH (a)-[:knows]->(b) WHERE a = a RETURN a, b`)
				var bs []string
				for _, r := range cy.Rows {
					if r["a"].Text == "alice" {
						bs = append(bs, r["b"].String())
					}
				}
				got := col(s, "b")
				slices.Sort(bs)
				slices.Sort(got)
				if !slices.Equal(got, bs) {
					t.Fatalf("sparql %v vs cypher %v", got, bs)
				}
				return
			}
			a, b := col(s, c.col), col(cy, c.col)
			if c.name != "order" {
				slices.Sort(a)
				slices.Sort(b)
			}
			if !slices.Equal(a, b) {
				t.Fatalf("sparql %v vs cypher %v", a, b)
			}
			if fmt.Sprint(s.Preds) != fmt.Sprint(cy.Preds) && (c.name == "property" || c.name == "count") {
				t.Fatalf("preds differ: %v vs %v", s.Preds, cy.Preds)
			}
		})
	}
}
