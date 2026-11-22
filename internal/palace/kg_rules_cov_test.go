package palace

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRulesCov_DatalogParseErrors(t *testing.T) {
	cases := []struct {
		name, src string
		want      error
		msg       string
	}{
		{"unterminated string", `p(X) :- q(X, "abc).`, ErrRuleParse, "1:14: unterminated string"},
		{"bad char", "p(X) :- q(X, Y) @ .", ErrRuleParse, "unexpected character '@'"},
		{"fact", "p(a, b).", ErrRuleParse, "facts are not supported"},
		{"contains head", "contains(X, Y) :- q(X, Y).", ErrRuleParse, "contains is a builtin"},
		{"contains negated", "p(X) :- q(X, Y), not contains(X, Y).", ErrRuleParse, "contains cannot be negated"},
		{"contains arity", "p(X) :- q(X, Y), contains(X).", ErrRuleParse, "contains takes 2 arguments"},
		{"missing dot", "p(X) :- q(X, Y)", ErrRuleParse, `expected ".", found end of input`},
		{"missing implies", "p(X) q(X, Y).", ErrRuleParse, `expected ":-"`},
		{"head not ident", "5 :- q(X, Y).", ErrRuleParse, "expected a predicate name"},
		{"args not term", "p(X) :- q(X, ).", ErrRuleParse, "expected a term"},
		{"args no open paren", "p :- q(X, Y).", ErrRuleParse, `expected "("`},
		{"args no close paren", "p(X :- q(X, Y).", ErrRuleParse, `expected ")"`},
		{"bad comparison op", "p(X) :- q(X, Y), X ~ Y.", ErrRuleParse, "unexpected character"},
		{"comparison missing op", "p(X) :- q(X, Y), X Y.", ErrRuleParse, "expected a comparison operator"},
		{"comparison missing rhs", "p(X) :- q(X, Y), X < .", ErrRuleParse, "expected a term"},
		{"head triple", "triple(X, Y, Z) :- q(X, Y).", ErrRuleParse, "cannot be defined by a rule"},
		{"head arity clash", "p(X) :- q(X, Y).\np(X, Y) :- q(X, Y).", ErrRuleParse, "used with 1 and 2 arguments"},
		{"stored arity", "p(X) :- q(X, Y, Z).", ErrRuleParse, "q takes 2 arguments, got 3"},
		{"triple arity", "p(X) :- triple(X, Y).", ErrRuleParse, "triple takes 3 arguments, got 2"},
		{"unsafe head", "p(X, Z) :- q(X, Y).", ErrUnsafeRule, "variable Z in the head"},
		{"unsafe builtin", "p(X) :- q(X, Y), Z > 3.", ErrUnsafeRule, "variable Z in a builtin"},
		{"unsafe negation", "p(X) :- q(X, Y), not r(X, Z).", ErrUnsafeRule, "variable Z in a negated atom"},
		{"unsafe constraint", ":- q(X, Y), Z < 1.", ErrUnsafeRule, "variable Z"},
		{"unstratified", "p(X, Y) :- q(X, Y), not r(X, Y).\nr(X, Y) :- q(X, Y), not p(X, Y).", ErrUnstratified, "negation inside a recursive cycle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseDatalog(tc.src)
			mustErr(t, err, tc.want)
			if !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %q, want substring %q", err, tc.msg)
			}
		})
	}
	_, err := ParseRules("prolog", "p(X) :- q(X, Y).")
	mustErr(t, err, ErrRuleParse)
	if !strings.Contains(err.Error(), `unknown syntax "prolog"`) {
		t.Fatalf("err = %v", err)
	}
	// syntax is trimmed and case-insensitive; empty means datalog
	for _, syn := range []string{"", " DataLog ", "N3"} {
		text := "p(X, Y) :- q(X, Y)."
		if strings.TrimSpace(strings.ToLower(syn)) == "n3" {
			text = "@prefix : <u:> . { ?x :q ?y } => { ?x :p ?y } ."
		}
		if p, err := ParseRules(syn, text); err != nil || p.Rules() != 1 {
			t.Fatalf("syntax %q: %v %v", syn, p, err)
		}
	}
}

func TestRulesCov_ProgramAccessors(t *testing.T) {
	p := mustParse(t, "r(X, Y) :- q(X, Y).\n:- q(X, X).\n:- r(X, Y), X = Y.")
	if p.Rules() != 1 || p.Constraints() != 2 || p.HasNegation() {
		t.Fatalf("accessors = %d %d %v", p.Rules(), p.Constraints(), p.HasNegation())
	}
	neg := mustParse(t, "r(X, Y) :- q(X, Y), not s(X, Y).")
	if !neg.HasNegation() {
		t.Fatal("HasNegation false")
	}
}

func TestRulesCov_LexerLiteralsAndIdents(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("big"), Ent("n"), Int(1), Valid{})
	assert1(t, kg, Ent("a-b"), Ent("has:colon"), Str("tab\there\nnl \"q\""), Valid{})
	assert1(t, kg, Ent("x"), Ent("score"), Num(-2.5), Valid{})
	p := mustParse(t, `
% a comment
esc(X) :- has:colon(X, "tab\there\nnl \"q\"").
neg(X) :- score(X, -2.5).
huge(X) :- n(X, 1), n(X, 99999999999999999999) .
`)
	v := kg.Now()
	equalStrings(t, answers(solveAll(t, v, p, "esc(X)"), "X"), []string{"a-b"})
	equalStrings(t, answers(solveAll(t, v, p, "neg(X)"), "X"), []string{"x"})
	if got := solveAll(t, v, p, "huge(X)"); len(got) != 0 {
		t.Fatalf("huge = %v", got)
	}
	// numValue falls back to a float for an out-of-range integer
	if v := numValue("99999999999999999999"); v.Kind != KindNumber || v.Num < 9.9e19 {
		t.Fatalf("numValue = %v", v)
	}
	if v := numValue("42"); v != Int(42) {
		t.Fatalf("numValue int = %v", v)
	}
}

func TestRulesCov_BuiltinsOnMixedKinds(t *testing.T) {
	t1 := TimeVal(time.UnixMilli(1000))
	t2 := TimeVal(time.UnixMilli(2000))
	mk := func(op string, a, b Value) bool {
		l := &literal{kind: litBuiltin, pred: op, args: []term{constTerm(a), constTerm(b)}}
		return evalBuiltin(l, nil)
	}
	cases := []struct {
		op   string
		a, b Value
		want bool
	}{
		{"=", Int(1), Num(1), true},
		{"=", Int(1), Int(2), false},
		{"!=", Int(1), Num(1), false},
		{"!=", Str("a"), Ent("a"), true},
		{"<", Int(1), Num(1.5), true},
		{"<=", Int(2), Int(2), true},
		{">", Num(2.5), Int(2), true},
		{">=", Int(2), Num(2.5), false},
		{"<", Str("a"), Str("b"), true},
		{">", Ent("b"), Ent("a"), true},
		{"<", Str("a"), Ent("b"), true}, // text kinds compare together
		{"<", Int(1), Str("a"), false},  // number vs text: unordered
		{"<", Str("a"), Int(1), false},
		{"<", t1, t2, true},
		{">=", t2, t1, true},
		{"<", t1, Int(5), false}, // time vs int: unordered
		{"<", Bool(true), Bool(false), false},
		{"contains", Str("hello world"), Str("lo w"), true},
		{"contains", Ent("hello"), Str("xyz"), false},
		{"contains", Int(5), Str("5"), false},
		{"contains", Str("5"), Int(5), false},
		{"??", Int(1), Int(2), false},
	}
	for _, tc := range cases {
		if got := mk(tc.op, tc.a, tc.b); got != tc.want {
			t.Errorf("%v %s %v = %v, want %v", tc.a, tc.op, tc.b, got, tc.want)
		}
	}
	// a variable argument reads the environment
	l := &literal{kind: litBuiltin, pred: "<", args: []term{{v: 0}, constTerm(Int(5))}}
	if !evalBuiltin(l, []Value{Int(3)}) || evalBuiltin(l, []Value{Int(9)}) {
		t.Fatal("variable builtin")
	}
}

func TestRulesCov_FragmentOrderingHelpers(t *testing.T) {
	i := func(n int64) *int64 { return &n }
	if cmpPtr(nil, nil, true) != 0 || cmpPtr(i(1), i(1), true) != 0 {
		t.Fatal("equal ptrs")
	}
	if cmpPtr(nil, i(1), true) != -1 || cmpPtr(nil, i(1), false) != 1 {
		t.Fatal("nil a")
	}
	if cmpPtr(i(1), nil, true) != 1 || cmpPtr(i(1), nil, false) != -1 {
		t.Fatal("nil b")
	}
	if cmpPtr(i(1), i(2), true) != -1 || cmpPtr(i(3), i(2), true) != 1 {
		t.Fatal("values")
	}
	a := frag{sup: []int64{1}, from: i(1), to: i(5)}
	b := frag{sup: []int64{1}, from: i(2), to: i(5)}
	if cmpFrag(a, b) != -1 || cmpFrag(b, a) != 1 {
		t.Fatal("from order")
	}
	c := frag{sup: []int64{1}, from: i(1), to: i(6)}
	if cmpFrag(a, c) != -1 {
		t.Fatal("to order")
	}
	// an open end sorts after a closed one
	d := frag{sup: []int64{1}, from: i(1)}
	if cmpFrag(a, d) != -1 || cmpFrag(d, a) != 1 {
		t.Fatal("open end")
	}
	e := frag{sup: []int64{2}, from: i(1), to: i(5)}
	if cmpFrag(a, e) != -1 || cmpFrag(a, a) != 0 {
		t.Fatal("support order")
	}
	// combine: disjoint intervals yield no fragment, tightening works
	if _, ok := combine(frag{from: i(0), to: i(2)}, frag{from: i(2), to: i(4)}); ok {
		t.Fatal("empty intersection kept")
	}
	got, ok := combine(frag{sup: []int64{3}, from: i(0), to: i(9)}, frag{sup: []int64{1, 3}, from: i(2), to: i(4)})
	if !ok || *got.from != 2 || *got.to != 4 || len(got.sup) != 2 {
		t.Fatalf("combine = %+v", got)
	}
	// dominates: containment, equal interval tie-break by smaller support
	if !dominates(d, a) || dominates(a, d) {
		t.Fatal("dominates containment")
	}
	if !dominates(a, e) || dominates(e, a) {
		t.Fatal("dominates tie-break")
	}
}

func TestRulesCov_SolveGoalForms(t *testing.T) {
	kg, _ := newEngine(t)
	orgGraph(t, kg)
	assert1(t, kg, Ent("bob"), Ent("manager"), Str("literal-boss"), Valid{})
	p := mustParse(t, orgRules)
	v := kg.Now()
	// triple/3 reads any predicate
	if got := answers(solveAll(t, v, p, "triple(carol, P, O)"), "P"); len(got) != 1 || got[0] != "manager" {
		t.Fatalf("triple goal = %v", got)
	}
	// repeated variable and constants in the goal
	assert1(t, kg, Ent("loop"), Ent("manager"), Ent("loop"), Valid{})
	equalStrings(t, answers(solveAll(t, v, p, "manager(X, X)"), "X"), []string{"loop"})
	if got := solveAll(t, v, p, "manager(carol, bob)"); len(got) != 1 || len(got[0].Bindings) != 0 {
		t.Fatalf("ground goal = %v", got)
	}
	// anonymous variables are not bound in the answer
	sols := solveAll(t, v, p, "manager(_, _)")
	if len(sols) == 0 || len(sols[0].Bindings) != 0 {
		t.Fatalf("anon goal = %v", sols)
	}
	// trailing dot is accepted
	if got := solveAll(t, v, p, "manager(carol, M)."); len(got) != 1 {
		t.Fatalf("dotted goal = %v", got)
	}
	// goal errors
	ctx := context.Background()
	for goal, msg := range map[string]string{
		"manager(X, Y) extra": "after the goal",
		"manager(X)":          "manager takes 2 arguments",
		"reports(X, Y, Z)":    "reports takes 2 arguments",
		"triple(X, Y)":        "triple takes 3 arguments",
		"5":                   "expected a predicate name",
		`manager(X, "oops`:    "unterminated string",
	} {
		_, err := v.Solve(ctx, p, goal, RuleOptions{})
		mustErr(t, err, ErrRuleParse)
		if !strings.Contains(err.Error(), msg) {
			t.Errorf("goal %q: err = %v, want %q", goal, err, msg)
		}
	}
}

func TestRulesCov_ContainsAndComparisonsEndToEnd(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("title"), Str("Quarterly report"), Valid{})
	assert1(t, kg, Ent("b"), Ent("title"), Str("Notes"), Valid{})
	assert1(t, kg, Ent("a"), Ent("pages"), Int(12), Valid{})
	assert1(t, kg, Ent("b"), Ent("pages"), Num(3.5), Valid{})
	assert1(t, kg, Ent("a"), Ent("seen"), TimeVal(time.UnixMilli(5000)), Valid{})
	assert1(t, kg, Ent("b"), Ent("seen"), TimeVal(time.UnixMilli(9000)), Valid{})
	p := mustParse(t, `
rep(X, T)   :- title(X, T), contains(T, "report").
long(X, N)  :- pages(X, N), N >= 10.
short(X, N) :- pages(X, N), N <= 10.
late(X, S)  :- seen(X, S), S > 6.
mixed(X, Y) :- title(X, T), pages(Y, N), T < N.
eqn(X, N)   :- pages(X, N), N = 12.
`)
	v := kg.Now()
	equalStrings(t, answers(solveAll(t, v, p, "rep(X, T)"), "X"), []string{"a"})
	equalStrings(t, answers(solveAll(t, v, p, "long(X, N)"), "X"), []string{"a"})
	equalStrings(t, answers(solveAll(t, v, p, "short(X, N)"), "X"), []string{"b"})
	// a time literal is not comparable to an int literal in the program
	if got := solveAll(t, v, p, "late(X, S)"); len(got) != 0 {
		t.Fatalf("time > int = %v", got)
	}
	if got := solveAll(t, v, p, "mixed(X, Y)"); len(got) != 0 {
		t.Fatalf("text < number = %v", got)
	}
	equalStrings(t, answers(solveAll(t, v, p, "eqn(X, N)"), "X"), []string{"a"})
}

func TestRulesCov_CheckWithNegationAndBudgets(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("a"), Ent("owner"), Ent("x"), Valid{})
	assert1(t, kg, Ent("b"), Ent("owner"), Ent("y"), Valid{})
	assert1(t, kg, Ent("a"), Ent("approved"), Ent("x"), Valid{})
	p := mustParse(t, ":- owner(S, O), not approved(S, O).")
	ctx := context.Background()
	vs, err := kg.Now().Check(ctx, p, RuleOptions{})
	if err != nil || len(vs) != 1 || vs[0].Bindings["S"] != Ent("b") || len(vs[0].Witnesses) != 1 {
		t.Fatalf("Check = %+v %v", vs, err)
	}
	if !strings.Contains(vs[0].Rule, "not approved") {
		t.Fatalf("rule text = %q", vs[0].Rule)
	}
	_, err = kg.Now().Check(ctx, p, RuleOptions{Budget: Budget{MaxRows: 1}})
	mustErr(t, err, ErrBudget)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = kg.Now().Check(cctx, p, RuleOptions{}); err == nil {
		t.Fatal("canceled check succeeded")
	}
	if _, err = kg.Now().Solve(cctx, p, "owner(X, Y)", RuleOptions{}); err == nil {
		t.Fatal("canceled solve succeeded")
	}
	// several constraints sort deterministically by rule text
	two := mustParse(t, ":- owner(S, O), O = y.\n:- owner(S, O), O = x.")
	vs, err = kg.Now().Check(ctx, two, RuleOptions{})
	if err != nil || len(vs) != 2 || vs[0].Rule > vs[1].Rule {
		t.Fatalf("ordering = %+v %v", vs, err)
	}
}

func TestRulesCov_ManyStepsHitContextCheck(t *testing.T) {
	kg, _ := newEngine(t)
	for i := 0; i < 40; i++ {
		assert1(t, kg, Ent("n"+string(rune('a'+i%26))+string(rune('a'+i/26))), Ent("edge"), Ent("hub"), Valid{})
	}
	p := mustParse(t, "pair(X, Y) :- edge(X, H), edge(Y, H).")
	sols := solveAll(t, kg.Now(), p, "pair(X, Y)")
	if len(sols) != 40*40 {
		t.Fatalf("pairs = %d, want 1600", len(sols))
	}
	// MaxFacts bounds the derived tuples of a heavy join
	_, err := kg.Now().Solve(context.Background(), p, "pair(X, Y)", RuleOptions{MaxFacts: 100})
	mustErr(t, err, ErrBudget)
	// MaxBytes budget
	_, err = kg.Now().Solve(context.Background(), p, "pair(X, Y)", RuleOptions{Budget: Budget{MaxBytes: 64}})
	mustErr(t, err, ErrBudget)
}
