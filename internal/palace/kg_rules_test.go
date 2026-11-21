package palace

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, text string) *Program {
	t.Helper()
	p, err := ParseDatalog(text)
	if err != nil {
		t.Fatalf("ParseDatalog: %v", err)
	}
	return p
}

func solveAll(t *testing.T, v View, p *Program, goal string) []Solution {
	t.Helper()
	sols, err := v.Solve(context.Background(), p, goal, RuleOptions{})
	if err != nil {
		t.Fatalf("Solve(%s): %v", goal, err)
	}
	return sols
}

// answers renders the bindings of variable name, sorted and de-duplicated.
func answers(sols []Solution, name string) []string {
	var out []string
	for _, s := range sols {
		out = append(out, s.Bindings[name].String())
	}
	sort.Strings(out)
	return slices.Compact(out)
}

func equalStrings(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func orgGraph(t *testing.T, kg *KnowledgeGraph) (e1, e2, e3 int64) {
	t.Helper()
	e1 = assert1(t, kg, Ent("bob"), Ent("manager"), Ent("alice"), Valid{})
	e2 = assert1(t, kg, Ent("carol"), Ent("manager"), Ent("bob"), Valid{})
	e3 = assert1(t, kg, Ent("dave"), Ent("manager"), Ent("bob"), Valid{})
	return e1, e2, e3
}

const orgRules = `
% who reports to whom
reports(X, Y) :- manager(X, Y).
reports(X, Z) :- manager(X, Y), reports(Y, Z).
`

func TestRules_TransitiveClosure(t *testing.T) {
	kg, _ := newEngine(t)
	e1, e2, _ := orgGraph(t, kg)
	p := mustParse(t, orgRules)

	sols := solveAll(t, kg.Now(), p, "reports(X, alice)")
	equalStrings(t, answers(sols, "X"), []string{"bob", "carol", "dave"})
	for _, s := range sols {
		if s.Bindings["X"].Text == "carol" && !slices.Equal(s.Support, []int64{e1, e2}) {
			t.Fatalf("carol support = %v, want [%d %d]", s.Support, e1, e2)
		}
	}
	equalStrings(t, answers(solveAll(t, kg.Now(), p, "reports(carol, Y)"), "Y"), []string{"alice", "bob"})
	if got := solveAll(t, kg.Now(), p, "reports(alice, Y)"); len(got) != 0 {
		t.Fatalf("alice reports to %v", got)
	}
	// A stored goal reads the view directly.
	equalStrings(t, answers(solveAll(t, kg.Now(), p, "manager(carol, M)"), "M"), []string{"bob"})
}

func TestRules_RecursionTerminatesOnCycle(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("edge"), Ent("b"), Valid{})
	assert1(t, kg, Ent("b"), Ent("edge"), Ent("c"), Valid{})
	assert1(t, kg, Ent("c"), Ent("edge"), Ent("a"), Valid{})
	p := mustParse(t, "path(X,Y) :- edge(X,Y). path(X,Z) :- path(X,Y), edge(Y,Z).")
	equalStrings(t, answers(solveAll(t, kg.Now(), p, "path(a, Y)"), "Y"), []string{"a", "b", "c"})
}

func TestRules_StratifiedNegation(t *testing.T) {
	kg, _ := newEngine(t)
	orgGraph(t, kg)
	p := mustParse(t, orgRules+`
peer(X, Y) :- reports(X, M), reports(Y, M), X != Y, not reports(X, Y).
`)
	equalStrings(t, answers(solveAll(t, kg.Now(), p, "peer(carol, Y)"), "Y"), []string{"dave"})
	if !p.HasNegation() {
		t.Fatal("HasNegation = false")
	}
	// Two strata: negation of a derived predicate sits above it.
	if p.stratum["peer"] <= p.stratum["reports"] {
		t.Fatalf("strata peer=%d reports=%d", p.stratum["peer"], p.stratum["reports"])
	}
}

func TestRules_Builtins(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("ann"), Ent("age"), Int(30), Valid{})
	assert1(t, kg, Ent("bo"), Ent("age"), Int(17), Valid{})
	assert1(t, kg, Ent("cy"), Ent("age"), Num(17.5), Valid{})
	assert1(t, kg, Ent("ann"), Ent("note"), Str("likes tea"), Valid{})
	assert1(t, kg, Ent("bo"), Ent("note"), Str("likes coffee"), Valid{})
	p := mustParse(t, `
adult(X) :- age(X, A), A >= 18.
minor(X) :- age(X, A), A < 18.
tea(X) :- note(X, N), contains(N, "tea").
same(X) :- age(X, A), A = 30.
`)
	equalStrings(t, answers(solveAll(t, kg.Now(), p, "adult(X)"), "X"), []string{"ann"})
	equalStrings(t, answers(solveAll(t, kg.Now(), p, "minor(X)"), "X"), []string{"bo", "cy"})
	equalStrings(t, answers(solveAll(t, kg.Now(), p, "tea(X)"), "X"), []string{"ann"})
	equalStrings(t, answers(solveAll(t, kg.Now(), p, "same(X)"), "X"), []string{"ann"})
	// triple reads any predicate.
	equalStrings(t, answers(solveAll(t, kg.Now(), mustParse(t, "any(S) :- triple(S, age, 17)."), "any(S)"), "S"), []string{"bo"})
}

func TestRules_RejectsUnsafeAndUnstratified(t *testing.T) {
	cases := []struct {
		name, text string
		want       error
	}{
		{"head var unbound", "p(X, Y) :- q(X, Z).", ErrUnsafeRule},
		{"negated var unbound", "p(X) :- q(X, a), not r(X, Y).", ErrUnsafeRule},
		{"builtin var unbound", "p(X) :- q(X, a), Y > 3.", ErrUnsafeRule},
		{"only negative body", "p(X) :- not q(X, a).", ErrUnsafeRule},
		{"constraint unsafe", ":- q(X, a), not r(Y, a).", ErrUnsafeRule},
		{"self negation", "p(X) :- q(X, a), not p(X).", ErrUnstratified},
		{"mutual negation", "p(X) :- q(X, a), not r(X). r(X) :- q(X, a), not p(X).", ErrUnstratified},
		{"fact rejected", "p(a, b).", ErrRuleParse},
		{"missing dot", "p(X) :- q(X, a)", ErrRuleParse},
		{"bad token", "p(X) :- q(X, a) $.", ErrRuleParse},
		{"arity clash", "p(X) :- q(X, a). p(X, Y) :- q(X, a), q(Y, a).", ErrRuleParse},
		{"stored arity", "p(X) :- q(X, a, b).", ErrRuleParse},
		{"triple head", "triple(X, a, b) :- q(X, b).", ErrRuleParse},
		{"unterminated string", `p(X) :- q(X, "abc).`, ErrRuleParse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDatalog(tc.text)
			mustErr(t, err, tc.want)
		})
	}
	// Stratified negation across strata is accepted.
	if _, err := ParseDatalog("p(X) :- q(X, a). r(X) :- q(X, a), not p(X)."); err != nil {
		t.Fatalf("stratified program rejected: %v", err)
	}
	_, err := ParseRules("sparql", "")
	mustErr(t, err, ErrRuleParse)
}

func TestRules_ConstraintViolationsWithWitnesses(t *testing.T) {
	kg, _ := newEngine(t)
	ok := assert1(t, kg, Ent("ann"), Ent("age"), Int(30), Valid{})
	bad := assert1(t, kg, Ent("bo"), Ent("age"), Int(-4), Valid{})
	p := mustParse(t, `
:- age(X, A), A < 0.
:- manager(X, X).
`)
	vs, err := kg.Now().Check(context.Background(), p, RuleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 {
		t.Fatalf("violations = %+v", vs)
	}
	v := vs[0]
	if v.Bindings["X"] != Ent("bo") || v.Bindings["A"] != Int(-4) || !slices.Equal(v.Witnesses, []int64{bad}) {
		t.Fatalf("violation = %+v (ok eid %d)", v, ok)
	}
	if !strings.HasPrefix(v.Rule, ":- age(X, A)") {
		t.Fatalf("rule = %q", v.Rule)
	}

	// A constraint over a derived predicate carries the stored witnesses.
	e1 := assert1(t, kg, Ent("bob"), Ent("manager"), Ent("alice"), Valid{})
	e2 := assert1(t, kg, Ent("alice"), Ent("manager"), Ent("bob"), Valid{})
	p = mustParse(t, orgRules+":- reports(X, X).")
	vs, err = kg.Now().Check(context.Background(), p, RuleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) == 0 {
		t.Fatal("cycle not reported")
	}
	for _, v := range vs {
		if !slices.Equal(v.Witnesses, []int64{e1, e2}) {
			t.Fatalf("witnesses = %v, want [%d %d]", v.Witnesses, e1, e2)
		}
	}
}

func TestRules_ConstraintIgnoresNonOverlappingWitnesses(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("x"), Ent("a"), Ent("y"), Valid{From: day("2026-01-01"), To: day("2026-02-01")})
	assert1(t, kg, Ent("x"), Ent("b"), Ent("y"), Valid{From: day("2026-03-01")})
	vs, err := kg.Now().Check(context.Background(), mustParse(t, ":- a(X, Y), b(X, Y)."), RuleOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 0 {
		t.Fatalf("violations for statements never valid together: %+v", vs)
	}
}

func TestRules_PointwiseValidTime(t *testing.T) {
	kg, _ := newEngine(t)
	a := assert1(t, kg, Ent("x"), Ent("a"), Ent("y"), Valid{From: day("2026-01-01"), To: day("2026-03-01")})
	c := assert1(t, kg, Ent("x"), Ent("c"), Ent("y"), Valid{From: day("2026-02-01"), To: day("2026-04-01")})
	assert1(t, kg, Ent("x"), Ent("d"), Ent("y"), Valid{From: day("2026-06-01")}) // never overlaps a
	p := mustParse(t, "both(X, Y) :- a(X, Y), c(X, Y). never(X, Y) :- a(X, Y), d(X, Y).")

	sols := solveAll(t, kg.Now(), p, "both(x, y)")
	if len(sols) != 1 {
		t.Fatalf("both = %+v", sols)
	}
	s := sols[0]
	if !s.ValidFrom.Equal(*day("2026-02-01")) || !s.ValidTo.Equal(*day("2026-03-01")) {
		t.Fatalf("interval = [%v, %v), want [Feb, Mar)", s.ValidFrom, s.ValidTo)
	}
	if !slices.Equal(s.Support, []int64{a, c}) {
		t.Fatalf("support = %v", s.Support)
	}
	if got := solveAll(t, kg.Now(), p, "never(x, y)"); len(got) != 0 {
		t.Fatalf("empty intersection derived: %+v", got)
	}
}

// proofsGraph asserts the facts of a two-proof program in the given order.
func proofsGraph(t *testing.T, kg *KnowledgeGraph, order []int) {
	t.Helper()
	facts := []func(){
		func() {
			tx(t, kg, func(w *KGTx) error {
				_, err := w.Create(Ent("x"), Ent("a"), Ent("y"), Valid{From: day("2026-01-01"), To: day("2026-02-01")})
				return err
			})
		},
		func() {
			assert1(t, kg, Ent("x"), Ent("b"), Ent("y"), Valid{From: day("2026-03-01"), To: day("2026-04-01")})
		},
		func() { assert1(t, kg, Ent("y"), Ent("e"), Ent("z"), Valid{}) },
		func() {
			tx(t, kg, func(w *KGTx) error {
				_, err := w.Create(Ent("x"), Ent("a"), Ent("y"), Valid{From: day("2026-01-15"), To: day("2026-01-20")})
				return err
			})
		},
	}
	for _, i := range order {
		facts[i]()
	}
}

// canonical renders solutions independent of eid numbering: supports are
// spelled as the statements they point at.
func canonical(t *testing.T, kg *KnowledgeGraph, sols []Solution) []string {
	t.Helper()
	var out []string
	for _, s := range sols {
		var sup []string
		for _, e := range s.Support {
			st, err := kg.Get(context.Background(), e)
			if err != nil || st == nil {
				t.Fatalf("Get(%d): %v", e, err)
			}
			sup = append(sup, fmt.Sprintf("%s %s %s %v %v", st.Subject, st.Predicate, st.Object, st.ValidFrom, st.ValidTo))
		}
		sort.Strings(sup)
		out = append(out, fmt.Sprintf("%v|%v|%v|%v", s.Bindings, s.ValidFrom, s.ValidTo, sup))
	}
	sort.Strings(out)
	return out
}

func TestRules_DisjointProofsKeepBothFragmentsInAnyOrder(t *testing.T) {
	text := `
r(X, Y) :- a(X, Y).
r(X, Y) :- b(X, Y).
s(X, Z) :- r(X, Y), e(Y, Z).
`
	var want []string
	for i, order := range [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 1, 3, 0}} {
		kg, _ := newEngine(t)
		proofsGraph(t, kg, order)
		p := mustParse(t, text)
		sols := solveAll(t, kg.Now(), p, "r(x, y)")
		// a [Jan,Feb) and b [Mar,Apr) are disjoint, the second a [Jan15,Jan20)
		// is inside the first and redundant: two fragments.
		if len(sols) != 2 {
			t.Fatalf("order %v: r(x,y) = %d fragments, want 2", order, len(sols))
		}
		got := append(canonical(t, kg, sols), canonical(t, kg, solveAll(t, kg.Now(), p, "s(x, Z)"))...)
		if len(got) != 4 {
			t.Fatalf("order %v: got %v", order, got)
		}
		if i == 0 {
			want = got
		} else if !slices.Equal(got, want) {
			t.Fatalf("order %v:\n got %v\nwant %v", order, got, want)
		}
	}
}

func TestRules_RejectsDeepDisjointFragments(t *testing.T) {
	kg, _ := newEngine(t)
	for m := 1; m <= 5; m++ {
		from := time.Date(2026, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 0, 10)
		assert1(t, kg, Ent("x"), Ent("a"), Ent("y"), Valid{From: &from, To: &to})
	}
	_, err := kg.Now().Solve(context.Background(), mustParse(t, "r(X,Y) :- a(X,Y)."), "r(X, Y)", RuleOptions{MaxFragments: 3})
	mustErr(t, err, ErrBudget)
	if _, err := kg.Now().Solve(context.Background(), mustParse(t, "r(X,Y) :- a(X,Y)."), "r(X, Y)", RuleOptions{MaxFragments: 5}); err != nil {
		t.Fatal(err)
	}
}

func TestRules_Budget(t *testing.T) {
	kg, _ := newEngine(t)
	orgGraph(t, kg)
	p := mustParse(t, orgRules)
	ctx := context.Background()

	_, err := kg.Now().Solve(ctx, p, "reports(X, Y)", RuleOptions{MaxFacts: 2})
	mustErr(t, err, ErrBudget)
	_, err = kg.Now().Solve(ctx, p, "reports(X, Y)", RuleOptions{Budget: Budget{MaxRows: 2}})
	mustErr(t, err, ErrBudget)
	if _, err := kg.Now().Solve(ctx, p, "reports(X, Y)", RuleOptions{MaxFacts: 100}); err != nil {
		t.Fatal(err)
	}

	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = kg.Now().Solve(cctx, p, "reports(X, Y)", RuleOptions{})
	mustErr(t, err, context.Canceled)
	_, err = kg.Now().Check(cctx, p, RuleOptions{})
	mustErr(t, err, context.Canceled)
}

func TestRules_ViewScope(t *testing.T) {
	kg, _ := newEngine(t)
	e1, _, _ := orgGraph(t, kg)
	p := mustParse(t, orgRules)
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(e1); return err })
	// The retracted statement is gone from Now and present in AsOf.
	equalStrings(t, answers(solveAll(t, kg.Now(), p, "reports(carol, Y)"), "Y"), []string{"bob"})
	equalStrings(t, answers(solveAll(t, kg.AsOf(3), p, "reports(carol, Y)"), "Y"), []string{"alice", "bob"})
}

// --- stored programs and Materialize ---

func storeProgram(t *testing.T, kg *KnowledgeGraph, name, syntax, text string) int64 {
	t.Helper()
	var eid int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		eid, err = w.StoreProgram(name, syntax, text)
		return err
	})
	return eid
}

func materialize1(t *testing.T, kg *KnowledgeGraph, name string, v View) *MaterializeReport {
	t.Helper()
	rep, err := kg.Materialize(context.Background(), TxOptions{Author: "test"}, name, v, RuleOptions{})
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	return rep
}

func liveCount(t *testing.T, kg *KnowledgeGraph, pred string) int {
	t.Helper()
	sts, err := kg.Now().Match(context.Background(), Pattern{Predicate: new(Ent(pred))})
	if err != nil {
		t.Fatal(err)
	}
	return len(sts)
}

func TestRules_StoreAndLoadProgram(t *testing.T) {
	kg, _ := newEngine(t)
	ctx := context.Background()
	_, _, err := kg.LoadProgram(ctx, "org")
	mustErr(t, err, ErrProgramNotFound)

	eid := storeProgram(t, kg, "org", "datalog", orgRules)
	if again := storeProgram(t, kg, "org", "datalog", orgRules); again != eid {
		t.Fatalf("storing the same text again gave e%d, want e%d", again, eid)
	}
	p, got, err := kg.LoadProgram(ctx, "org")
	if err != nil || got != eid || p.Rules() != 2 {
		t.Fatalf("LoadProgram = %v, e%d, %v", p, got, err)
	}
	// A bad program is not stored.
	mustErr(t, txErr(kg, func(w *KGTx) error { _, err := w.StoreProgram("bad", "datalog", "p(X) :- not q(X, a)."); return err }), ErrUnsafeRule)
	_, _, err = kg.LoadProgram(ctx, "bad")
	mustErr(t, err, ErrProgramNotFound)
	// New text supersedes the old statement.
	var next int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		next, err = w.StoreProgram("org", "datalog", orgRules+"\nboss(X) :- reports(Y, X).")
		return err
	})
	if next == eid || live(t, kg, eid) {
		t.Fatalf("old program e%d live=%v, new e%d", eid, live(t, kg, eid), next)
	}
	p, got, _ = kg.LoadProgram(ctx, "org")
	if got != next || p.Rules() != 3 {
		t.Fatalf("after supersede: e%d, %d rules", got, p.Rules())
	}
}

func TestRules_MaterializeRequiresStoredProgram(t *testing.T) {
	kg, _ := newEngine(t)
	_, err := kg.Materialize(context.Background(), TxOptions{}, "nope", kg.Now(), RuleOptions{})
	mustErr(t, err, ErrProgramNotFound)
}

func TestRules_MaterializeRejectsNegation(t *testing.T) {
	kg, _ := newEngine(t)
	orgGraph(t, kg)
	storeProgram(t, kg, "neg", "datalog", orgRules+"peer(X,Y) :- reports(X,M), reports(Y,M), X != Y, not reports(X,Y).")
	_, err := kg.Materialize(context.Background(), TxOptions{}, "neg", kg.Now(), RuleOptions{})
	mustErr(t, err, ErrNotMaterializable)
	if n := liveCount(t, kg, "reports"); n != 0 {
		t.Fatalf("rejected program wrote %d statements", n)
	}
	storeProgram(t, kg, "unary", "datalog", "adult(X) :- age(X, A), A >= 18.")
	_, err = kg.Materialize(context.Background(), TxOptions{}, "unary", kg.Now(), RuleOptions{})
	mustErr(t, err, ErrNotMaterializable)
}

func TestRules_MaterializeDerivesAndIsIdempotent(t *testing.T) {
	kg, _ := newEngine(t)
	e1, e2, _ := orgGraph(t, kg)
	peid := storeProgram(t, kg, "org", "datalog", orgRules)

	rep := materialize1(t, kg, "org", kg.Now())
	if rep.Program != peid || len(rep.Derived) != 5 || len(rep.Existing)+len(rep.Widened)+len(rep.Reconciled) != 0 {
		t.Fatalf("first run = %+v", rep)
	}
	if n := liveCount(t, kg, "reports"); n != 5 {
		t.Fatalf("reports statements = %d", n)
	}
	// carol -> alice rests on both manager statements and the program.
	var carolAlice *Statement
	for _, eid := range rep.Derived {
		st, _ := kg.Get(context.Background(), eid)
		if st.Subject == Ent("carol") && st.Object == Ent("alice") {
			carolAlice = st
		}
	}
	if carolAlice == nil {
		t.Fatal("carol reports alice not derived")
	}
	ctx := context.Background()
	derivedPred := sysPred(predDerivedFrom)
	links, err := kg.Now().Match(ctx, Pattern{Subject: new(StmtRef(carolAlice.EID)), Predicate: &derivedPred, IncludeEngine: true})
	if err != nil {
		t.Fatal(err)
	}
	var sup []int64
	for _, l := range links {
		sup = append(sup, l.Object.Int)
	}
	slices.Sort(sup)
	if want := []int64{e1, e2, peid}; !slices.Equal(sup, want) {
		t.Fatalf("support = %v, want %v", sup, want)
	}

	again := materialize1(t, kg, "org", kg.Now())
	if len(again.Derived)+len(again.Widened)+len(again.Reconciled) != 0 || len(again.Existing) != 5 {
		t.Fatalf("second run = %+v", again)
	}
	if len(again.Tx.Asserted)+len(again.Tx.Retracted)+len(again.Tx.Superseded) != 0 {
		t.Fatalf("second run wrote statements: %+v", again.Tx)
	}
	if n := liveCount(t, kg, "reports"); n != 5 {
		t.Fatalf("reports statements after rerun = %d", n)
	}
}

func TestRules_OwnConclusionsAreNotInput(t *testing.T) {
	kg, _ := newEngine(t)
	for _, e := range [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"d", "e"}} {
		assert1(t, kg, Ent(e[0]), Ent("next"), Ent(e[1]), Valid{})
	}
	// hop/2 reads every predicate, so without the filter its own conclusions
	// would chain into longer hops on the next run.
	storeProgram(t, kg, "hops", "datalog", "hop(S, Z) :- triple(S, P, Y), triple(Y, Q, Z).")
	first := materialize1(t, kg, "hops", kg.Now())
	if len(first.Derived) != 3 {
		t.Fatalf("first run derived %d, want 3", len(first.Derived))
	}
	second := materialize1(t, kg, "hops", kg.Now())
	if len(second.Derived) != 0 || len(second.Reconciled) != 0 || len(second.Existing) != 3 {
		t.Fatalf("second run = %+v", second)
	}
	if n := liveCount(t, kg, "hop"); n != 3 {
		t.Fatalf("hop statements = %d, want 3", n)
	}
}

func TestRules_ConclusionsFallWithPremiseAndProgram(t *testing.T) {
	kg, _ := newEngine(t)
	e1, _, _ := orgGraph(t, kg)
	peid := storeProgram(t, kg, "org", "datalog", orgRules)
	rep := materialize1(t, kg, "org", kg.Now())

	// Premise e1: everything resting on bob -> alice falls (bob->alice,
	// carol->alice, dave->alice); carol->bob and dave->bob remain.
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(e1); return err })
	if n := liveCount(t, kg, "reports"); n != 2 {
		t.Fatalf("reports after premise retract = %d, want 2", n)
	}
	// Program: the rest falls.
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(peid); return err })
	if n := liveCount(t, kg, "reports"); n != 0 {
		t.Fatalf("reports after program retract = %d, want 0", n)
	}
	for _, eid := range rep.Derived {
		if live(t, kg, eid) {
			t.Fatalf("conclusion e%d still live", eid)
		}
	}

	// Superseding a program withdraws its conclusions too.
	kg2, _ := newEngine(t)
	orgGraph(t, kg2)
	storeProgram(t, kg2, "org", "datalog", orgRules)
	materialize1(t, kg2, "org", kg2.Now())
	storeProgram(t, kg2, "org", "datalog", orgRules+"\nboss(X, Y) :- reports(Y, X).")
	if n := liveCount(t, kg2, "reports"); n != 0 {
		t.Fatalf("reports after program supersede = %d, want 0", n)
	}
	rep2 := materialize1(t, kg2, "org", kg2.Now())
	if len(rep2.Derived) != 10 {
		t.Fatalf("rematerialized %d, want 10", len(rep2.Derived))
	}
}

func TestRules_ReconcileOnlyOnPlainNowView(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("x"), Ent("a"), Ent("y"), Valid{From: day("2026-01-01"), To: day("2026-02-01")})
	storeProgram(t, kg, "r", "datalog", "r(X, Y) :- a(X, Y). r(X, Y) :- b(X, Y).")
	first := materialize1(t, kg, "r", kg.Now())
	if len(first.Derived) != 1 {
		t.Fatalf("first = %+v", first)
	}
	old := first.Derived[0]

	// An unbounded b makes the bounded a-proof redundant.
	assert1(t, kg, Ent("x"), Ent("b"), Ent("y"), Valid{})

	// A restricted view only adds.
	restricted := materialize1(t, kg, "r", kg.Now().ValidAt(*day("2026-01-10")))
	if len(restricted.Reconciled) != 0 || len(restricted.Derived) != 1 || !live(t, kg, old) {
		t.Fatalf("restricted run = %+v, old live=%v", restricted, live(t, kg, old))
	}
	grouped := materialize1(t, kg, "r", kg.Now().InGraph("nowhere"))
	if len(grouped.Reconciled) != 0 || !live(t, kg, old) {
		t.Fatalf("graph-scoped run reconciled: %+v", grouped)
	}
	// The plain Now view retracts what is no longer derivable.
	plain := materialize1(t, kg, "r", kg.Now())
	if !slices.Equal(plain.Reconciled, []int64{old}) || live(t, kg, old) {
		t.Fatalf("plain run = %+v, old live=%v", plain, live(t, kg, old))
	}
	if n := liveCount(t, kg, "r"); n != 1 {
		t.Fatalf("r statements = %d, want 1", n)
	}
}

func TestRules_MaterializeWidensNarrowConclusionKeepingSupport(t *testing.T) {
	kg, _ := newEngine(t)
	a := assert1(t, kg, Ent("x"), Ent("a"), Ent("y"), Valid{})
	peid := storeProgram(t, kg, "r", "datalog", "r(X, Y) :- a(X, Y).")

	// A conclusion with the right support but a narrower interval, as an
	// older run or a hand-written Derive could leave behind.
	var narrow int64
	tx(t, kg, func(w *KGTx) error {
		var err error
		narrow, _, err = w.Derive(Ent("x"), Ent("r"), Ent("y"), Valid{From: day("2026-01-01"), To: day("2026-02-01")}, []int64{a, peid})
		return err
	})
	rep := materialize1(t, kg, "r", kg.Now())
	if len(rep.Widened) != 1 || len(rep.Derived) != 0 || len(rep.Reconciled) != 0 {
		t.Fatalf("run = %+v", rep)
	}
	if live(t, kg, narrow) {
		t.Fatal("narrow conclusion still live")
	}
	wide, _ := kg.Get(context.Background(), rep.Widened[0])
	if wide.ValidFrom != nil || wide.ValidTo != nil || !wide.Live() {
		t.Fatalf("widened = %+v", wide)
	}
	derived := sysPred(predDerivedFrom)
	links, _ := kg.Now().Match(context.Background(), Pattern{Subject: new(StmtRef(wide.EID)), Predicate: &derived, IncludeEngine: true})
	var sup []int64
	for _, l := range links {
		sup = append(sup, l.Object.Int)
	}
	slices.Sort(sup)
	if !slices.Equal(sup, []int64{a, peid}) {
		t.Fatalf("widened support = %v, want [%d %d]", sup, a, peid)
	}
	// And it still falls with its premise.
	tx(t, kg, func(w *KGTx) error { _, err := w.Retract(a); return err })
	if live(t, kg, wide.EID) {
		t.Fatal("widened conclusion survived its premise")
	}
}

func TestRules_MaterializeTwoDisjointProofs(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("x"), Ent("a"), Ent("y"), Valid{From: day("2026-01-01"), To: day("2026-02-01")})
	assert1(t, kg, Ent("x"), Ent("b"), Ent("y"), Valid{From: day("2026-03-01"), To: day("2026-04-01")})
	storeProgram(t, kg, "r", "datalog", "r(X, Y) :- a(X, Y). r(X, Y) :- b(X, Y).")
	rep := materialize1(t, kg, "r", kg.Now())
	if len(rep.Derived) != 2 {
		t.Fatalf("derived %d, want one statement per fragment", len(rep.Derived))
	}
	if again := materialize1(t, kg, "r", kg.Now()); len(again.Derived) != 0 || len(again.Existing) != 2 {
		t.Fatalf("rerun = %+v", again)
	}
}

func TestRules_MaterializeBudget(t *testing.T) {
	kg, _ := newEngine(t)
	orgGraph(t, kg)
	storeProgram(t, kg, "org", "datalog", orgRules)
	_, err := kg.Materialize(context.Background(), TxOptions{}, "org", kg.Now(), RuleOptions{MaxFacts: 1})
	mustErr(t, err, ErrBudget)
	if n := liveCount(t, kg, "reports"); n != 0 {
		t.Fatalf("budget failure wrote %d statements", n)
	}
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = kg.Materialize(cctx, TxOptions{}, "org", kg.Now(), RuleOptions{})
	mustErr(t, err, context.Canceled)
}

func TestRules_MaterializeRejectsPastViews(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("manager"), Ent("b"), Valid{})
	tx(t, kg, func(w *KGTx) error {
		_, err := w.StoreProgram("r", "datalog", "reports(X, Y) :- manager(X, Y).")
		return err
	})
	for _, v := range []View{kg.AsOf(1), kg.History()} {
		_, err := kg.Materialize(context.Background(), TxOptions{}, "r", v, RuleOptions{})
		mustErr(t, err, ErrNotMaterializable)
	}
}
