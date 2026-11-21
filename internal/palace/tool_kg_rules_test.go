package palace

import (
	"context"
	"testing"
)

func TestToolKG_Rules(t *testing.T) {
	p := newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	for _, in := range []KGAddToolInput{
		{Subject: "alice", Predicate: "manager", Object: "bob"},
		{Subject: "bob", Predicate: "manager", Object: "carol"},
		{Subject: "dave", Predicate: "age", Object: "-3", Literal: true},
	} {
		palaceKGAddHandler(ctx, p, in)
	}
	prog := "reports(X, Y) :- manager(X, Y).\nreports(X, Z) :- manager(X, Y), reports(Y, Z)."
	out, _ := palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Program: prog, Goal: "reports(alice, Y)"})
	mustContain(t, out.Content, "2 solution(s)", "Y=bob", "Y=carol", "from e1 e2")

	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "store", Name: "org", Program: prog})
	mustContain(t, out.Content, "Stored program org as e")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "materialize", Name: "org", DryRun: true})
	mustContain(t, out.Content, "Dry run, nothing written", "3 new")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "materialize", Name: "org"})
	mustContain(t, out.Content, "Materialized org: 3 new")
	q, _ := palaceKGQueryHandler(ctx, p, KGQueryToolInput{Entity: "alice"})
	mustContain(t, q.Content, "reports", "carol")

	n3 := "@prefix : <urn:x#> . { ?a :manager ?b } => { ?a :boss_of ?b } ."
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Syntax: "n3", Program: n3, Goal: "boss_of(X, carol)"})
	mustContain(t, out.Content, "1 solution(s)", "X=bob")

	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "check", Program: ":- age(X, A), A < 0."})
	// age is stored as text, so the numeric comparison is unknown and nothing violates.
	mustContain(t, out.Content, "No constraint violations.")
	out, _ = palaceKGRulesHandler(ctx, p, KGRulesToolInput{Action: "solve", Program: "p(X) :- q(X, Y", Goal: "p(X)"})
	mustContain(t, out.Content, "Error:")
}
