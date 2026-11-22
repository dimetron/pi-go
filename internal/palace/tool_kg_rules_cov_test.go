package palace

import (
	"context"
	"testing"
)

func rulesTool(t *testing.T) (*Palace, mockToolCtx) {
	t.Helper()
	p := newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	for _, in := range []KGAddToolInput{
		{Subject: "alice", Predicate: "manager", Object: "bob"},
		{Subject: "bob", Predicate: "manager", Object: "carol"},
		{Subject: "carol", Predicate: "manager", Object: "carol"},
	} {
		if out, _ := palaceKGAddHandler(ctx, p, in); out.Content == "" {
			t.Fatal("add failed")
		}
	}
	return p, ctx
}

const toolOrgRules = "reports(X, Y) :- manager(X, Y).\nreports(X, Z) :- manager(X, Y), reports(Y, Z)."

func TestToolKGRulesCov_ArgumentErrors(t *testing.T) {
	p, ctx := rulesTool(t)
	cases := []struct {
		name string
		in   KGRulesToolInput
		want string
	}{
		{"store no name", KGRulesToolInput{Action: "store", Program: toolOrgRules}, "Error: name and program are required"},
		{"store no program", KGRulesToolInput{Action: "store", Name: "x"}, "Error: name and program are required"},
		{"store bad program", KGRulesToolInput{Action: "store", Name: "x", Program: "p(X) :- q(X, Y"}, "Error:"},
		{"store bad n3", KGRulesToolInput{Action: "store", Name: "x", Syntax: "n3", Program: "{ ?a ?p ?b } => { ?a ?p ?b } ."}, "variable predicates are not supported"},
		{"solve no goal", KGRulesToolInput{Action: "solve", Program: toolOrgRules}, "Error: goal is required"},
		{"solve elpi no goal", KGRulesToolInput{Action: "solve", Syntax: "elpi", Program: "p."}, "Error: goal is required"},
		{"solve no program or name", KGRulesToolInput{Action: "solve", Goal: "reports(X, Y)"}, "program or name is required"},
		{"check no program or name", KGRulesToolInput{Action: "check"}, "program or name is required"},
		{"solve unknown program", KGRulesToolInput{Action: "solve", Name: "ghost", Goal: "reports(X, Y)"}, "no such program: ghost"},
		{"check unknown program", KGRulesToolInput{Action: "check", Name: "ghost"}, "no such program: ghost"},
		{"solve bad goal", KGRulesToolInput{Action: "solve", Program: toolOrgRules, Goal: "reports(X)"}, "reports takes 2 arguments"},
		{"solve unknown syntax", KGRulesToolInput{Action: "solve", Syntax: "prolog", Program: toolOrgRules, Goal: "reports(X, Y)"}, `unknown syntax "prolog"`},
		{"materialize no name", KGRulesToolInput{Action: "materialize"}, "Error: name of a stored program is required"},
		{"materialize unknown", KGRulesToolInput{Action: "materialize", Name: "ghost"}, "no such program: ghost"},
		{"unknown action", KGRulesToolInput{Action: "explode"}, "Error: action must be store, solve, check or materialize"},
		{"empty action", KGRulesToolInput{}, "Error: action must be store, solve, check or materialize"},
	}
	for _, tc := range cases {
		out, err := palaceKGRulesHandler(ctx, p, tc.in)
		if err != nil {
			t.Errorf("%s: handler error %v (errors belong in the content)", tc.name, err)
		}
		mustContain(t, out.Content, tc.want)
	}
}

func TestToolKGRulesCov_SolveByStoredNameAndCheck(t *testing.T) {
	p, ctx := rulesTool(t)
	out, _ := palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "store", Name: "org", Program: toolOrgRules + "\n:- manager(X, X)."})
	mustContain(t, out.Content, "Stored program org as e")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Name: "org", Goal: "reports(alice, Y)"})
	mustContain(t, out.Content, "2 solution(s)", "Y=bob", "Y=carol")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Name: "org", Goal: "reports(carol, dave)"})
	mustContain(t, out.Content, "No solutions.")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "check", Name: "org"})
	mustContain(t, out.Content, "1 violation(s)", ":- manager(X, X).", "X=carol", "(facts e3)")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "check", Program: ":- manager(X, X), X = alice."})
	mustContain(t, out.Content, "No constraint violations.")
	// inline program wins over name
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Name: "org", Program: "boss(X, Y) :- manager(X, Y).", Goal: "boss(alice, Y)"})
	mustContain(t, out.Content, "1 solution(s)", "Y=bob")
	// a goal that is invalid for a stored program
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "check", Program: "p(X) :- manager(X, Y"})
	mustContain(t, out.Content, "Error:")
}

func TestToolKGRulesCov_StoreN3AndMaterializeGraph(t *testing.T) {
	p, ctx := rulesTool(t)
	n3 := "@prefix : <urn:x#> . { ?a :manager ?b } => { ?a :boss_of ?b } ."
	out, _ := palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "store", Name: "n3org", Syntax: "n3", Program: n3})
	mustContain(t, out.Content, "Stored program n3org as e")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "materialize", Name: "n3org", DryRun: true})
	mustContain(t, out.Content, "Dry run, nothing written. Would materialize n3org: 3 new", "0 widened", "0 withdrawn")
	if n := liveCount(t, p.KG(), "boss_of"); n != 0 {
		t.Fatalf("dry run wrote %d statements", n)
	}
	// an n3 program stored by name is solved without restating the syntax
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Name: "n3org", Goal: "boss_of(alice, Y)"})
	mustContain(t, out.Content, "1 solution(s)", "Y=bob")
	// a graph with no facts sees nothing
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Name: "n3org", Goal: "boss_of(X, Y)", Graph: "elsewhere"})
	mustContain(t, out.Content, "No solutions.")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "materialize", Name: "n3org"})
	mustContain(t, out.Content, "Materialized n3org: 3 new, 0 already derived")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "materialize", Name: "n3org"})
	mustContain(t, out.Content, "Materialized n3org: 0 new, 3 already derived")
	// materializing a program with negation is refused with a message
	palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "store", Name: "neg", Program: toolOrgRules + "\nlone(X, Y) :- manager(X, Y), not reports(Y, X)."})
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "materialize", Name: "neg"})
	mustContain(t, out.Content, "Error:", "negation")
}

func TestToolKGRulesCov_Elpi(t *testing.T) {
	p, ctx := rulesTool(t)
	// carol manages herself: without tabling the cyclic goal never ends, and
	// the row cap turns that into a message instead of a hang
	loopy := "reports X Y :- tm.triple X manager Y.\nreports X Z :- tm.triple X manager Y, reports Y Z."
	out0, _ := palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Syntax: "elpi", Program: loopy, Goal: "reports alice Y"})
	mustContain(t, out0.Content, "Error:", "out of fuel", "more than 1000 solutions")
	p = newToolPalace(t)
	for _, in := range []KGAddToolInput{
		{Subject: "alice", Predicate: "manager", Object: "bob"},
		{Subject: "bob", Predicate: "manager", Object: "carol"},
	} {
		palaceKGAddHandler(ctx, p, in)
	}
	prog := "reports X Y :- tm.triple X manager Y.\nreports X Z :- tm.triple X manager Y, reports Y Z."
	out, _ := palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Syntax: "elpi", Program: prog, Goal: "reports alice Y"})
	mustContain(t, out.Content, "2 solution(s)", "steps:", "- Y=bob", "- Y=carol")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Syntax: "elpi", Program: prog, Goal: "reports dave Y"})
	mustContain(t, out.Content, "No solutions.")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Syntax: "elpi", Program: "reports X Y :-", Goal: "reports a Y"})
	mustContain(t, out.Content, "Error:", "elpi program")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Syntax: "elpi", Program: prog, Goal: "nope a"})
	mustContain(t, out.Content, "Error:", "unknown predicate nope/1")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Syntax: "elpi", Program: prog, Goal: "reports (alice"})
	mustContain(t, out.Content, "Error:", "expected ')'")
	// a graph view applies to elpi too
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Syntax: "elpi", Program: prog, Goal: "reports alice Y", Graph: "elsewhere"})
	mustContain(t, out.Content, "No solutions.")
}

func TestToolKGRulesCov_FormatHelpers(t *testing.T) {
	if got := formatBindings(map[string]Value{"B": Int(2), "A": Ent("x")}); got != "A=x, B=2" {
		t.Fatalf("formatBindings = %q", got)
	}
	if got := formatBindings(nil); got != "" {
		t.Fatalf("empty bindings = %q", got)
	}
	if got := formatEIDs([]int64{3, 1}); got != "e3 e1" {
		t.Fatalf("formatEIDs = %q", got)
	}
}
