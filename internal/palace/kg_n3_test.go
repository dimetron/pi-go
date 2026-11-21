package palace

import (
	"context"
	"slices"
	"testing"
)

const orgN3 = `
@prefix : <http://example.org/> .
@prefix ex: <http://example.org/> .
# transitive reporting line
{ ?x :manager ?y } => { ?x :reports ?y } .
{ ?x ex:manager ?y . ?y :reports ?z } => { ?x :reports ?z } .
`

func TestN3_AgreesWithDatalog(t *testing.T) {
	kg, _ := newEngine(t)
	orgGraph(t, kg)
	n3, err := ParseN3(orgN3)
	if err != nil {
		t.Fatal(err)
	}
	dl := mustParse(t, orgRules)
	for _, goal := range []string{"reports(X, alice)", "reports(carol, Y)", "reports(X, Y)"} {
		a := canonical(t, kg, solveAll(t, kg.Now(), n3, goal))
		b := canonical(t, kg, solveAll(t, kg.Now(), dl, goal))
		if len(a) == 0 || !slices.Equal(a, b) {
			t.Fatalf("%s:\n n3 %v\n dl %v", goal, a, b)
		}
	}
}

func TestN3_BuiltinsAgreeWithDatalog(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("ann"), Ent("age"), Int(30), Valid{})
	assert1(t, kg, Ent("bo"), Ent("age"), Int(17), Valid{})
	assert1(t, kg, Ent("cy"), Ent("age"), Int(30), Valid{})
	n3 := `
@prefix : <urn:x#> .
@prefix math: <http://www.w3.org/2000/10/swap/math#> .
{ ?x :age ?a . ?a math:greaterThan 18 } => { ?x :adult ?a } .
{ ?x :age ?a . ?a math:lessThan 18 } => { ?x :minor ?a } .
{ ?x :age ?a . ?y :age ?a . ?x math:notEqualTo ?y } => { ?x :twin ?y } .
`
	dl := `
adult(X, A) :- age(X, A), A > 18.
minor(X, A) :- age(X, A), A < 18.
twin(X, Y) :- age(X, A), age(Y, A), X != Y.
`
	pn, err := ParseN3(n3)
	if err != nil {
		t.Fatal(err)
	}
	pd := mustParse(t, dl)
	for _, goal := range []string{"adult(X, A)", "minor(X, A)", "twin(X, Y)"} {
		a := canonical(t, kg, solveAll(t, kg.Now(), pn, goal))
		b := canonical(t, kg, solveAll(t, kg.Now(), pd, goal))
		if len(a) == 0 || !slices.Equal(a, b) {
			t.Fatalf("%s:\n n3 %v\n dl %v", goal, a, b)
		}
	}
}

func TestN3_RejectsWhatItDoesNotSupport(t *testing.T) {
	cases := map[string]string{
		"fact":                "@prefix : <u:> . :a :p :b .",
		"undeclared prefix":   "{ ?x :p ?y } => { ?x :q ?y } .",
		"variable predicate":  "@prefix : <u:> . { ?x ?p ?y } => { ?x :q ?y } .",
		"log builtin":         "@prefix : <u:> . @prefix log: <l:> . { ?x log:includes ?y } => { ?x :q ?y } .",
		"nested formula":      "@prefix : <u:> . { ?x :p { } } => { ?x :q ?x } .",
		"missing arrow":       "@prefix : <u:> . { ?x :p ?y } { ?x :q ?y } .",
		"builtin head":        "@prefix : <u:> . @prefix math: <m:> . { ?x :p ?y } => { ?x math:lessThan ?y } .",
		"unterminated iri":    "@prefix : <u: .",
		"bare word predicate": "@prefix : <u:> . { ?x p ?y } => { ?x :q ?y } .",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseN3(text)
			mustErr(t, err, ErrRuleParse)
		})
	}
	// Safety applies to N3 as well.
	_, err := ParseN3("@prefix : <u:> . { ?x :p ?y } => { ?x :q ?z } .")
	mustErr(t, err, ErrUnsafeRule)
	_, err = ParseRules("n3", "@prefix : <u:> . { ?x :p ?y } => { ?x :q ?z } .")
	mustErr(t, err, ErrUnsafeRule)
}

func TestN3_MultiTripleHeadAndStoredProgram(t *testing.T) {
	kg, _ := newEngine(t)
	orgGraph(t, kg)
	text := `
@prefix : <urn:x#> .
{ ?x :manager ?y } => { ?x :reports ?y . ?y :boss_of ?x } .
`
	p, err := ParseN3(text)
	if err != nil {
		t.Fatal(err)
	}
	if p.Rules() != 2 {
		t.Fatalf("rules = %d, want one per head triple", p.Rules())
	}
	equalStrings(t, answers(solveAll(t, kg.Now(), p, "boss_of(alice, X)"), "X"), []string{"bob"})

	// A stored N3 program keeps its syntax and materializes.
	storeProgram(t, kg, "n3org", "n3", text)
	loaded, _, err := kg.LoadProgram(context.Background(), "n3org")
	if err != nil || loaded.Rules() != 2 {
		t.Fatalf("LoadProgram = %v, %v", loaded, err)
	}
	rep := materialize1(t, kg, "n3org", kg.Now())
	if len(rep.Derived) != 6 {
		t.Fatalf("derived %d, want 6", len(rep.Derived))
	}
}
