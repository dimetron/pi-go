package palace

import (
	"strings"
	"testing"
)

func TestN3Cov_ParseErrorsCarryPositions(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"unterminated iri pos", "\n@prefix : <u: .", "2:11: unterminated IRI"},
		{"empty variable", "@prefix : <u:> . { ? :p ?y } => { ?y :q ?y } .", "empty variable name"},
		{"unterminated string", "@prefix : <u:> . { ?x :p \"abc } => { ?x :q ?x } .", "unterminated string"},
		{"unexpected char", "@prefix : <u:> . { ?x :p ?y } => { ?x :q ?y } ; ", "unexpected character ';'"},
		{"unsupported directive", "@base <u:> .", "unsupported directive @base"},
		{"prefix name", "@prefix <u:> <v:> .", "expected a prefix name like 'ex:'"},
		{"prefix name no colon", "@prefix ex <u:> .", "expected a prefix name like 'ex:'"},
		{"prefix iri", "@prefix ex: foo .", "expected <iri>"},
		{"prefix dot", "@prefix ex: <u:> {", "expected '.'"},
		{"prefix eof", "@prefix ex:", "expected <iri>, found end of input"},
		{"body not formula", "@prefix : <u:> . ?x :p ?y .", "facts are not supported"},
		{"head not formula", "@prefix : <u:> . { ?x :p ?y } => ?x .", "expected '{', found"},
		{"missing final dot", "@prefix : <u:> . { ?x :p ?y } => { ?x :q ?y }", "expected '.', found end of input"},
		{"bad token in formula", "@prefix : <u:> . { ?x :p ?y => ", "expected '.' or '}', found"},
		{"predicate not a name", "@prefix : <u:> . { ?x 5 ?y } => { ?x :q ?y } .", "expected a predicate, found"},
		{"term expected", "@prefix : <u:> . { ?x :p . } => { ?x :q ?x } .", "expected a term, found"},
		{"unsupported math builtin", "@prefix : <u:> . @prefix math: <m:> . { ?x :p ?y . ?x math:sum ?y } => { ?x :q ?y } .", "unsupported builtin math:sum"},
		{"builtin as term", "@prefix : <u:> . @prefix math: <m:> . { ?x :p math:lessThan } => { ?x :q ?x } .", "only valid as a predicate"},
		{"empty local name", "@prefix : <u:> . { ?x :p : } => { ?x :q ?x } .", "empty local name"},
		{"undeclared prefix named", "@prefix : <u:> . { ?x zz:p ?y } => { ?x :q ?y } .", `undeclared prefix "zz:"`},
		{"bare word", "@prefix : <u:> . { ?x :p foo } => { ?x :q ?x } .", "bare word"},
		{"nested formula position", "@prefix : <u:> . { ?x :p {", "nested formulas are not supported"},
		{"variable predicate", "@prefix : <u:> . { ?x ?p ?y } => { ?x :q ?y } .", "variable predicates are not supported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseN3(tc.src)
			mustErr(t, err, ErrRuleParse)
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %q, want substring %q", err, tc.want)
			}
		})
	}
}

func TestN3Cov_IRIFormsAndLiterals(t *testing.T) {
	kg, _ := newEngine(t)
	assert1(t, kg, Ent("ann"), Ent("age"), Int(30), Valid{})
	assert1(t, kg, Ent("ann"), Ent("name"), Str("Ann \"A\""), Valid{})
	assert1(t, kg, Ent("bo"), Ent("age"), Num(17.5), Valid{})
	assert1(t, kg, Ent("cy"), Ent("age"), Int(-4), Valid{})
	// predicates and entities written as <iri>, mixed with prefixed names
	p, err := ParseN3(`
# comment
@prefix ex: <http://example.org/> .
{ ?x <age> ?a . ?a <http://www.w3.org/2000/10/swap/math#greaterThan> 18 } => { ?x ex:grown ?a } .
{ ?x ex:age 17.5 } => { ?x ex:half ?x } .
{ ?x ex:age -4 } => { ?x ex:neg ?x } .
{ ?x ex:name "Ann \"A\"" } => { ?x ex:named <ann> } .
{ <ann> ex:age ?a } => { <ann> ex:seen ?a } .
`)
	if err != nil {
		t.Fatal(err)
	}
	v := kg.Now()
	if got := answers(solveAll(t, v, p, "half(X, X)"), "X"); len(got) != 1 || got[0] != "bo" {
		t.Fatalf("decimal literal = %v", got)
	}
	if got := answers(solveAll(t, v, p, "neg(X, X)"), "X"); len(got) != 1 || got[0] != "cy" {
		t.Fatalf("negative literal = %v", got)
	}
	if got := answers(solveAll(t, v, p, "named(X, Y)"), "Y"); len(got) != 1 || got[0] != "ann" {
		t.Fatalf("string literal + <iri> object = %v", got)
	}
	if got := answers(solveAll(t, v, p, "seen(ann, A)"), "A"); len(got) != 1 {
		t.Fatalf("<iri> subject = %v", got)
	}
}

func TestN3Cov_SwapPrefixNeedsNoDeclaration(t *testing.T) {
	// a swap namespace prefix is recognized without @prefix, and an
	// unsupported builtin in it is rejected rather than read as a predicate
	_, err := ParseN3("@prefix : <u:> . { ?x :p ?y . ?x log:includes ?y } => { ?x :q ?y } .")
	mustErr(t, err, ErrRuleParse)
	if !strings.Contains(err.Error(), "unsupported builtin log:includes") {
		t.Fatalf("err = %v", err)
	}
	// a supported builtin with no @prefix math: works
	p, err := ParseN3("@prefix : <u:> . { ?x :p ?y . ?y math:lessThan 5 } => { ?x :q ?y } .")
	if err != nil || p.Rules() != 1 {
		t.Fatalf("math without prefix: %v %v", p, err)
	}
}

func TestN3Cov_MultilineTokenPositions(t *testing.T) {
	_, err := ParseN3("@prefix : <u:> .\n\n{ ?x :p ?y }\n  => { ?x :q ?y }\n  ,")
	mustErr(t, err, ErrRuleParse)
	if !strings.Contains(err.Error(), "5:3") {
		t.Fatalf("err = %v, want position 5:3", err)
	}
}

func TestN3Cov_EmptyProgramAndEmptyFormulas(t *testing.T) {
	p, err := ParseN3("# nothing\n")
	if err != nil || p.Rules() != 0 {
		t.Fatalf("empty = %v %v", p, err)
	}
	// an empty head yields no rules; an empty body is a unconditional rule
	// that is unsafe only when the head has variables
	p, err = ParseN3("@prefix : <u:> . { ?x :p ?y } => { } .")
	if err != nil || p.Rules() != 0 {
		t.Fatalf("empty head = %v %v", p, err)
	}
	_, err = ParseN3("@prefix : <u:> . { } => { ?x :q ?y } .")
	mustErr(t, err, ErrUnsafeRule)
}
