package palace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// covRun runs a query and returns the error (nil on success).
func covRun(f *qfix, lang, text string) (*QueryResult, error) {
	return f.kg.Now().RunQuery(context.Background(), lang, text, QueryOptions{})
}

type covErrCase struct {
	name, q string
	// unsupported selects ErrUnsupportedQuery, otherwise ErrQueryParse.
	unsupported bool
	// at is a substring of the message (position "1:N" for parse errors).
	at string
}

func runErrCases(t *testing.T, lang string, cases []covErrCase) {
	t.Helper()
	f := newQfix(t)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := covRun(f, lang, c.q)
			if err == nil {
				t.Fatalf("%q: want an error", c.q)
			}
			want := ErrQueryParse
			if c.unsupported {
				want = ErrUnsupportedQuery
			}
			if !errors.Is(err, want) {
				t.Fatalf("%q: error %v is not %v", c.q, err, want)
			}
			if c.unsupported == errors.Is(err, ErrQueryParse) {
				t.Fatalf("%q: error %v matches both kinds", c.q, err)
			}
			if c.at != "" && !strings.Contains(err.Error(), c.at) {
				t.Fatalf("%q: error %q lacks %q", c.q, err, c.at)
			}
		})
	}
}

func TestSPARQLErrors(t *testing.T) {
	runErrCases(t, "sparql", []covErrCase{
		{name: "prefix-no-colon", q: `PREFIX ex <http://x/> SELECT * WHERE {}`, at: "1:8"},
		{name: "prefix-no-iri", q: `PREFIX ex: foo SELECT * WHERE {}`, at: "1:12"},
		{name: "base", q: `BASE <http://x/> SELECT * WHERE {}`, unsupported: true, at: "offset 0"},
		{name: "construct", q: `CONSTRUCT { ?a <p> ?b } WHERE { ?a <p> ?b }`, unsupported: true},
		{name: "ask", q: `ASK { ?a <p> ?b }`, unsupported: true},
		{name: "insert", q: `INSERT DATA { <a> <p> <b> }`, unsupported: true},
		{name: "not-select-word", q: `FOO ?x`, at: "1:1"},
		{name: "not-select-var", q: `?x SELECT`, at: "1:1"},
		{name: "empty", q: ``, at: "end of query"},
		{name: "reduced", q: `SELECT REDUCED ?x WHERE { ?x <p> ?y }`, unsupported: true},
		{name: "from", q: `SELECT ?x FROM <g> WHERE { ?x <p> ?y }`, unsupported: true},
		{name: "no-brace", q: `SELECT ?x ?y`, at: "end of query"},
		{name: "no-vars", q: `SELECT WHERE { ?x <p> ?y }`, at: "1:8"},
		{name: "proj-expr", q: `SELECT (?x AS ?y) WHERE { ?x <p> ?z }`, unsupported: true},
		{name: "sum", q: `SELECT (SUM(?x) AS ?y) WHERE { ?z <p> ?x }`, unsupported: true},
		{name: "count-no-paren", q: `SELECT (COUNT ?x) WHERE { ?z <p> ?x }`},
		{name: "count-distinct", q: `SELECT (COUNT(DISTINCT *) AS ?n) WHERE { ?z <p> ?x }`, unsupported: true},
		{name: "count-var", q: `SELECT (COUNT(?x) AS ?n) WHERE { ?z <p> ?x }`, unsupported: true},
		{name: "count-unclosed", q: `SELECT (COUNT(* ?n) WHERE { ?z <p> ?x }`},
		{name: "count-no-as", q: `SELECT (COUNT(*) ?n) WHERE { ?z <p> ?x }`, at: "expected AS"},
		{name: "count-as-nonvar", q: `SELECT (COUNT(*) AS n) WHERE { ?z <p> ?x }`, at: "variable after AS"},
		{name: "count-as-unclosed", q: `SELECT (COUNT(*) AS ?n ?m) WHERE { ?z <p> ?x }`},
		{name: "count-group", q: `SELECT ?a (COUNT(*) AS ?n) WHERE { ?a <p> ?x }`, unsupported: true},
		{name: "order-no-by", q: `SELECT ?x WHERE { ?x <p> ?y } ORDER ?x`, at: "expected BY"},
		{name: "order-empty", q: `SELECT ?x WHERE { ?x <p> ?y } ORDER BY`, at: "ORDER BY condition"},
		{name: "order-asc-no-paren", q: `SELECT ?x WHERE { ?x <p> ?y } ORDER BY ASC ?x`},
		{name: "order-asc-expr", q: `SELECT ?x WHERE { ?x <p> ?y } ORDER BY ASC(1)`, unsupported: true},
		{name: "order-desc-unclosed", q: `SELECT ?x WHERE { ?x <p> ?y } ORDER BY DESC(?x`},
		{name: "limit-word", q: `SELECT ?x WHERE { ?x <p> ?y } LIMIT x`, at: "expected an integer"},
		{name: "offset-word", q: `SELECT ?x WHERE { ?x <p> ?y } OFFSET "x"`, at: "expected an integer"},
		{name: "group-by", q: `SELECT ?x WHERE { ?x <p> ?y } GROUP BY ?x`, unsupported: true},
		{name: "having", q: `SELECT ?x WHERE { ?x <p> ?y } HAVING (1)`, unsupported: true},
		{name: "values-tail", q: `SELECT ?x WHERE { ?x <p> ?y } VALUES ?x { 1 }`, unsupported: true},
		{name: "trailing-junk", q: `SELECT ?x WHERE { ?x <p> ?y } foo`, at: "unexpected"},
		{name: "missing-close", q: `SELECT ?x WHERE { ?x <p> ?y`, at: "missing }"},
		{name: "graph-var", q: `SELECT ?x WHERE { GRAPH ?g { ?x <p> ?y } }`, unsupported: true},
		{name: "graph-number", q: `SELECT ?x WHERE { GRAPH 5 { ?x <p> ?y } }`, at: "graph name"},
		{name: "bind", q: `SELECT ?x WHERE { ?x <p> ?y BIND(1 AS ?z) }`, unsupported: true},
		{name: "minus", q: `SELECT ?x WHERE { ?x <p> ?y MINUS { ?x <q> ?y } }`, unsupported: true},
		{name: "service", q: `SELECT ?x WHERE { SERVICE <s> { ?x <p> ?y } }`, unsupported: true},
		{name: "subquery", q: `SELECT ?x WHERE { { SELECT ?x WHERE { ?x <p> ?y } } }`, unsupported: true},
		{name: "values-inline", q: `SELECT ?x WHERE { VALUES ?x { 1 } }`, unsupported: true},
		{name: "not-no-exists", q: `SELECT ?x WHERE { ?x <p> ?y FILTER NOT foo }`, at: "expected EXISTS"},
		{name: "exists", q: `SELECT ?x WHERE { ?x <p> ?y FILTER EXISTS { ?x <q> ?y } }`, unsupported: true},
		{name: "filter-unclosed", q: `SELECT ?x WHERE { ?x <p> ?y FILTER (?y > 1 }`},
		{name: "filter-empty", q: `SELECT ?x WHERE { ?x <p> ?y FILTER ( ) }`, at: "expected an expression"},
		{name: "filter-semicolon", q: `SELECT ?x WHERE { ?x <p> ?y FILTER ; }`, at: "expected an expression"},
		{name: "verb-missing", q: `SELECT ?x WHERE { ?x }`, at: "expected a predicate"},
		{name: "term-missing", q: `SELECT ?x WHERE { ?x <p> }`, at: "expected a term"},
		{name: "blank-pred", q: `SELECT ?x WHERE { ?x _:b ?y }`, unsupported: true},
		{name: "blank-subject", q: `SELECT ?x WHERE { _:b <p> ?x }`, unsupported: true},
		{name: "blank-bracket", q: `SELECT ?x WHERE { [ ] <p> ?x }`, unsupported: true},
		{name: "collection", q: `SELECT ?x WHERE { ( 1 ) <p> ?x }`, unsupported: true},
		{name: "a-shorthand", q: `SELECT ?x WHERE { ?x a <C> }`, unsupported: true},
		{name: "path-inverse", q: `SELECT ?x WHERE { ?x ^<p> ?y }`, unsupported: true},
		{name: "path-seq", q: `SELECT ?x WHERE { ?x <p>/<q> ?y }`, unsupported: true},
		{name: "path-plus", q: `SELECT ?x WHERE { ?x <p>+ ?y }`, unsupported: true},
		{name: "path-star-var-pred", q: `SELECT ?x WHERE { ?x * ?y }`, unsupported: true},
		{name: "pred-number", q: `SELECT ?x WHERE { ?x 5 ?y }`, at: "expected a predicate"},
		{name: "lang-tag", q: `SELECT ?x WHERE { ?x <p> "a"@en }`, unsupported: true},
		{name: "datatype", q: `SELECT ?x WHERE { ?x <p> "a"^^<t> }`, unsupported: true},
		{name: "long-string", q: `SELECT ?x WHERE { ?x <p> """a""" }`, unsupported: true, at: "offset 25"},
		{name: "bare-word-term", q: `SELECT ?x WHERE { ?x <p> foo }`, at: "unexpected"},
		{name: "sign-no-number", q: `SELECT ?x WHERE { ?x <p> -foo }`, at: "expected a number after -"},
		{name: "int-overflow", q: `SELECT ?x WHERE { ?x <p> 99999999999999999999 }`, at: "integer out of range"},
		{name: "star-unclosed", q: `SELECT ?x WHERE { << ?a <p> ?b <q> ?x }`},
		{name: "star-bad-verb", q: `SELECT ?x WHERE { << ?a 5 ?b >> <q> ?x }`, at: "expected a predicate"},
		{name: "unterminated-string", q: "SELECT ?x WHERE {\n ?x <p> \"abc }", at: "2:9"},
		{name: "empty-var", q: `SELECT ? WHERE { }`, at: "empty variable name"},
		{name: "bad-char", q: `SELECT ?x WHERE { ?x <p> ~ }`, at: "unexpected character"},
		{name: "arithmetic", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(?y + 1 > 2) }`, unsupported: true},
		{name: "in", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(?y IN (1)) }`, unsupported: true},
		{name: "not-in", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(?y NOT IN (1)) }`, unsupported: true},
		{name: "arith-after-cmp", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(?y > 1 * 2) }`, unsupported: true},
		{name: "unknown-func", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(strlen(?y) > 1) }`, unsupported: true},
		{name: "word-no-paren", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(foo) }`, at: "unexpected"},
		{name: "bound-nonvar", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(bound(3)) }`, at: "takes a variable"},
		{name: "bound-unclosed", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(bound(?y 1)) }`},
		{name: "regex-no-comma", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(regex(?y "a")) }`},
		{name: "regex-var-pattern", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(regex(?y, ?y)) }`, unsupported: true},
		{name: "regex-flags-type", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(regex(?y, "a", 3)) }`, at: "flags must be a string"},
		{name: "regex-unclosed", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(regex(?y, "a" 1)) }`},
		{name: "regex-bad-pattern", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(regex(?y, "(")) }`, at: "bad regular expression"},
		{name: "contains-no-comma", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(contains(?y "a")) }`},
		{name: "contains-no-second", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(contains(?y, )) }`, at: "expected an expression"},
		{name: "contains-unclosed", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(contains(?y, "a" 1)) }`},
		{name: "group-paren-unclosed", q: `SELECT ?x WHERE { ?x <p> ?y FILTER((?y > 1 ) }`},
		{name: "union-second-broken", q: `SELECT ?x WHERE { { ?x <p> ?y } UNION { ?x <q> }`},
		{name: "optional-broken", q: `SELECT ?x WHERE { OPTIONAL ?x }`},
		{name: "graph-body-broken", q: `SELECT ?x WHERE { GRAPH <g> ?x }`},
		{name: "filter-and-broken", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(?y > 1 && ) }`},
		{name: "filter-or-broken", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(?y > 1 || ) }`},
		{name: "filter-cmp-broken", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(?y > ) }`},
		{name: "filter-not-broken", q: `SELECT ?x WHERE { ?x <p> ?y FILTER(!) }`},
	})
}

func TestSPARQLSignedObjectAfterIRIIsPath(t *testing.T) {
	// "<p> +7" lexes as the path operator + after a predicate; it is rejected
	// as an unsupported property path rather than read as a signed number.
	f := newQfix(t)
	_, err := covRun(f, "sparql", `SELECT ?p WHERE { ?p <pos> +7 }`)
	if !errors.Is(err, ErrUnsupportedQuery) {
		t.Fatalf("err = %v", err)
	}
	var u *UnsupportedQueryError
	if !errors.As(err, &u) || u.Construct != "property path" || u.Pos != 27 {
		t.Fatalf("err = %#v", err)
	}
}

func TestSPARQLUnknownLanguage(t *testing.T) {
	f := newQfix(t)
	_, err := covRun(f, "gremlin", "g.V()")
	if !errors.Is(err, ErrQueryParse) || !strings.Contains(err.Error(), "gremlin") {
		t.Fatalf("err = %v", err)
	}
	// Language names are case-insensitive and trimmed.
	if _, err := covRun(f, "  SPARQL ", `SELECT * WHERE { ?a <age> ?b }`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.kg.Now().Select(context.Background(), nil, QueryOptions{}); !errors.Is(err, ErrQueryParse) {
		t.Fatalf("nil query: %v", err)
	}
}

func TestSPARQLLexAndTermForms(t *testing.T) {
	f := newQfix(t)
	assert1(t, f.kg, Ent("alice"), Ent("score"), Num(-2.5), Valid{})
	assert1(t, f.kg, Ent("bob"), Ent("score"), Num(1000), Valid{})
	assert1(t, f.kg, Ent("alice"), Ent("note"), Str("a\nb\t\"q\"\\"), Valid{})
	assert1(t, f.kg, Ent("alice"), Ent("flag"), Bool(false), Valid{})
	assert1(t, f.kg, Ent("alice"), Ent("neg"), Int(-7), Valid{})
	assert1(t, f.kg, Ent("alice"), Ent("pos"), Int(7), Valid{})

	cases := []struct {
		name, q, col string
		want         []string
	}{
		{"negative decimal", `SELECT ?p WHERE { ?p <score> -2.5 }`, "p", []string{"alice"}},
		{"exponent", `SELECT ?p WHERE { ?p <score> 1e3 }`, "p", []string{"bob"}},
		{"signed exponent", `SELECT ?p WHERE { ?p <score> 1.0E+3 }`, "p", []string{"bob"}},
		{"negative exponent no digits is int then word", `SELECT ?p WHERE { ?p <neg> -7 }`, "p", []string{"alice"}},
		{"explicit plus", `SELECT ?p WHERE { ?p <pos> ?n . FILTER(?n = +7) }`, "p", []string{"alice"}},
		{"escapes", "SELECT ?p WHERE { ?p <note> 'a\\nb\\t\"q\"\\\\' }", "p", []string{"alice"}},
		{"false literal", `SELECT ?p WHERE { ?p <flag> false }`, "p", []string{"alice"}},
		{"comment", "SELECT ?p # pick people\nWHERE { ?p <flag> false }", "p", []string{"alice"}},
		{"dollar var", `SELECT $p WHERE { $p <flag> false }`, "p", []string{"alice"}},
		{"trailing semicolons", `SELECT ?p WHERE { ?p <flag> false ;; <pos> 7 ; . }`, "p", []string{"alice"}},
		{"prefixed trailing dot", `PREFIX ex: <http://ex.org/> SELECT ?p WHERE { ?p ex:pos 7 . ex:bob ex:knows ?p . ?p ex:flag false.}`, "p", nil},
		{"iri with hash", `SELECT ?p WHERE { <http://ex.org/ns#alice> <pos> ?p }`, "p", []string{"7"}},
		{"iri trailing slash", `SELECT ?p WHERE { <http://ex.org/alice/> <pos> ?p }`, "p", []string{"7"}},
		{"colon only name", `SELECT ?p WHERE { :alice :pos ?p }`, "p", []string{"7"}},
		{"unicode ident", `SELECT ?p WHERE { ?p <pos> 7 . FILTER(?p != <ünï>) }`, "p", []string{"alice"}},
		{"parenthesised filter", `SELECT ?p WHERE { ?p <pos> ?n . FILTER((?n = 7)) }`, "p", []string{"alice"}},
		{"bare bound filter", `SELECT ?p WHERE { ?p <pos> ?n . FILTER bound(?n) }`, "p", []string{"alice"}},
		{"filter true", `SELECT ?p WHERE { ?p <pos> ?n . FILTER(true) }`, "p", []string{"alice"}},
		{"filter false", `SELECT ?p WHERE { ?p <pos> ?n . FILTER(false) }`, "p", nil},
		{"filter negative const", `SELECT ?p WHERE { ?p <pos> ?n . FILTER(-3 < ?n) }`, "p", []string{"alice"}},
		{"filter iri const", `SELECT ?p WHERE { ?p <knows> ?n . FILTER(?n = <carol> && ?p = <bob>) }`, "p", []string{"bob"}},
		{"filter not", `SELECT ?p WHERE { ?p <age> ?n . FILTER(!(?n > 26)) }`, "p", []string{"bob"}},
		{"double not", `SELECT ?p WHERE { ?p <age> ?n . FILTER(!!(?n > 26)) }`, "p", []string{"alice", "carol"}},
		{"or", `SELECT ?p WHERE { ?p <age> ?n . FILTER(?n < 26 || ?n > 40) }`, "p", []string{"bob", "carol"}},
		{"le", `SELECT ?p WHERE { ?p <age> ?n . FILTER(?n <= 30) }`, "p", []string{"alice", "bob"}},
		{"ge", `SELECT ?p WHERE { ?p <age> ?n . FILTER(?n >= 30) }`, "p", []string{"alice", "carol"}},
		{"ne", `SELECT ?p WHERE { ?p <age> ?n . FILTER(?n != 30) }`, "p", []string{"bob", "carol"}},
		{"int vs decimal", `SELECT ?p WHERE { ?p <age> ?n . FILTER(?n = 30.0) }`, "p", []string{"alice"}},
		{"regex on entity", `SELECT ?p WHERE { ?p <age> ?n . FILTER(regex(?p, "^c")) }`, "p", []string{"carol"}},
		{"regex on number is unknown", `SELECT ?p WHERE { ?p <age> ?n . FILTER(regex(?n, "3")) }`, "p", nil},
		{"contains non-string arg unknown", `SELECT ?p WHERE { ?p <name> ?n . FILTER(contains(?n, 3)) }`, "p", nil},
		{"strstarts false", `SELECT ?p WHERE { ?p <name> ?n . FILTER(strstarts(?n, "x")) }`, "p", nil},
		{"unbound var in filter is unknown", `SELECT ?p WHERE { ?p <name> ?n . FILTER(?zz = 1) }`, "p", nil},
		{"unbound var or true", `SELECT ?p WHERE { ?p <name> ?n . FILTER(?zz = 1 || true) }`, "p", []string{"alice", "bob"}},
		{"unbound var and true is unknown", `SELECT ?p WHERE { ?p <name> ?n . FILTER(?zz = 1 && true) }`, "p", nil},
		{"unbound var and false", `SELECT ?p WHERE { ?p <name> ?n . FILTER(!(?zz = 1 && false)) }`, "p", []string{"alice", "bob"}},
		{"not unknown stays unknown", `SELECT ?p WHERE { ?p <name> ?n . FILTER(!(?zz = 1)) }`, "p", nil},
		{"cross-kind ne is true", `SELECT ?p WHERE { ?p <name> ?n . FILTER(?n != 3) }`, "p", []string{"alice", "bob"}},
		{"cross-kind lt is unknown", `SELECT ?p WHERE { ?p <name> ?n . FILTER(?n < 3) }`, "p", nil},
		{"cross-kind eq is false", `SELECT ?p WHERE { ?p <name> ?n . FILTER(?n = 3) }`, "p", nil},
		{"bool compare", `SELECT ?p WHERE { ?p <flag> ?n . FILTER(?n = false) }`, "p", []string{"alice"}},
		{"entity compare lt", `SELECT ?p WHERE { ?p <knows> ?n . FILTER(?p < ?n) }`, "p", []string{"alice", "alice", "bob"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := f.run(t, "sparql", c.q)
			wantSorted(t, res, c.col, c.want...)
		})
	}
}

func TestSPARQLRDFStarAndGraph(t *testing.T) {
	f := newQfix(t)
	// Quoted triple: the statement bound by << >> is annotated.
	res := f.run(t, "sparql", `SELECT ?c WHERE { << <alice> <knows> <bob> >> <confidence> ?c }`)
	wantCol(t, res, "c", "0.9")
	// Nested quoted triple as a subject with a statement variable.
	res = f.run(t, "sparql", `SELECT ?s ?o WHERE { << ?s <knows> ?o >> <confidence> 0.9 }`)
	wantCol(t, res, "s", "alice")
	wantCol(t, res, "o", "bob")
	// Hidden _star variables do not leak into SELECT *.
	res = f.run(t, "sparql", `SELECT * WHERE { << ?s <knows> ?o >> <confidence> ?c }`)
	if len(res.Vars) != 3 {
		t.Fatalf("vars = %v", res.Vars)
	}
	// A statement bound by a literal is not a statement.
	res = f.run(t, "sparql", `SELECT ?s WHERE { << ?s <knows> ?o >> <confidence> ?c . ?c <knows> ?x }`)
	if len(res.Rows) != 0 {
		t.Fatalf("literal subject matched: %v", res.Rows)
	}
	// A graph that holds nothing yields no rows; Negative is set.
	res = f.run(t, "sparql", `SELECT ?s WHERE { GRAPH <g1> { ?s <knows> ?o } }`)
	if len(res.Rows) != 0 || !res.Negative {
		t.Fatalf("empty graph: %v negative=%v", res.Rows, res.Negative)
	}
	res = f.run(t, "sparql", `PREFIX ex: <http://ex/> SELECT ?s WHERE { GRAPH ex:g1 { ?s <knows> ?o } }`)
	if len(res.Rows) != 0 {
		t.Fatalf("prefixed graph: %v", res.Rows)
	}
}

func TestSPARQLModifiersAndCount(t *testing.T) {
	f := newQfix(t)
	cases := []struct {
		name, q, col string
		want         []string
	}{
		{"asc explicit", `SELECT ?p ?n WHERE { ?p <age> ?n } ORDER BY ASC(?n)`, "p", []string{"bob", "alice", "carol"}},
		{"desc keyword plain var", `SELECT ?p ?n WHERE { ?p <age> ?n } ORDER BY ?n`, "p", []string{"bob", "alice", "carol"}},
		{"mixed keys", `SELECT ?p ?n WHERE { ?p <knows> ?n } ORDER BY ?p DESC(?n)`, "n", []string{"carol", "bob", "carol", "alice"}},
		{"limit 0", `SELECT ?p WHERE { ?p <age> ?n } LIMIT 0`, "p", nil},
		{"offset past end", `SELECT ?p WHERE { ?p <age> ?n } OFFSET 10`, "p", nil},
		{"offset and limit", `SELECT ?p WHERE { ?p <age> ?n } ORDER BY ?n OFFSET 1 LIMIT 1`, "p", []string{"alice"}},
		{"limit before offset", `SELECT ?p WHERE { ?p <age> ?n } ORDER BY ?n LIMIT 1 OFFSET 2`, "p", []string{"carol"}},
		{"distinct", `SELECT DISTINCT ?p WHERE { ?p <knows> ?n }`, "p", []string{"alice", "bob", "dave"}},
		{"order by unbound first", `SELECT ?p ?n WHERE { ?p <age> ?x . OPTIONAL { ?p <name> ?n } } ORDER BY ?n`, "p", []string{"carol", "alice", "bob"}},
		{"order by unbound last desc", `SELECT ?p ?n WHERE { ?p <age> ?x . OPTIONAL { ?p <name> ?n } } ORDER BY DESC(?n)`, "p", []string{"bob", "alice", "carol"}},
		{"order mixed kinds", `SELECT ?o WHERE { ?s ?p ?o . FILTER(?p != <knows> && ?p != <confidence>) } ORDER BY ?o`, "o", []string{"25", "30", "41", "Alice", "Bob"}},
		{"order mixed kinds desc", `SELECT ?o WHERE { ?s ?p ?o . FILTER(?p != <knows> && ?p != <confidence>) } ORDER BY DESC(?o)`, "o", []string{"Bob", "Alice", "41", "30", "25"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := f.run(t, "sparql", c.q)
			wantCol(t, res, c.col, c.want...)
		})
	}

	res := f.run(t, "sparql", `SELECT (COUNT(*) AS ?n) WHERE { ?a <knows> ?b }`)
	wantCol(t, res, "n", "4")
	if !res.Negative {
		t.Fatal("COUNT is non-monotonic")
	}
	res = f.run(t, "sparql", `SELECT (COUNT(*) AS ?n) WHERE { ?a <nothing> ?b }`)
	wantCol(t, res, "n", "0")
	res = f.run(t, "sparql", `SELECT (COUNT(*) AS ?n) WHERE { ?a <knows> ?b . FILTER(?a = <alice>) } OFFSET 1`)
	if len(res.Rows) != 0 || !res.Negative {
		t.Fatalf("offset past the single count row: %v", res.Rows)
	}
	// OFFSET alone makes a query non-monotonic.
	res = f.run(t, "sparql", `SELECT ?p WHERE { ?p <age> ?n } OFFSET 1`)
	if !res.Negative {
		t.Fatal("OFFSET must mark the query negative")
	}
}

func TestSPARQLBudgetsAndCancellation(t *testing.T) {
	f := newQfix(t)
	queries := map[string]string{
		"triple":   `SELECT ?a ?b WHERE { ?a <knows> ?b }`,
		"optional": `SELECT ?a WHERE { ?a <age> ?x . OPTIONAL { ?a <knows> ?b } }`,
		"union":    `SELECT ?a WHERE { { ?a <knows> ?b } UNION { ?a <age> ?b } }`,
		"notex":    `SELECT ?a WHERE { ?a <age> ?x . FILTER NOT EXISTS { ?a <knows> ?b } }`,
		"graph":    `SELECT ?a WHERE { GRAPH <g> { ?a <knows> ?b } }`,
		"star":     `SELECT ?c WHERE { << ?a <knows> ?b >> <confidence> ?c }`,
	}
	for name, q := range queries {
		t.Run(name+"/rows", func(t *testing.T) {
			if name == "graph" {
				t.Skip("an empty graph reads no statements, so no budget is charged")
			}
			_, err := f.kg.Now().RunQuery(context.Background(), "sparql", q, QueryOptions{Budget: Budget{MaxRows: 1}})
			if !errors.Is(err, ErrBudget) {
				t.Fatalf("err = %v", err)
			}
		})
		t.Run(name+"/canceled", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := f.kg.Now().RunQuery(ctx, "sparql", q, QueryOptions{})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	t.Run("bytes", func(t *testing.T) {
		_, err := f.kg.Now().RunQuery(context.Background(), "sparql", `SELECT ?n WHERE { ?p <name> ?n }`, QueryOptions{Budget: Budget{MaxBytes: 4}})
		if !errors.Is(err, ErrBudget) || !strings.Contains(err.Error(), "bytes") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("within budget", func(t *testing.T) {
		res, err := f.kg.Now().RunQuery(context.Background(), "sparql", `SELECT ?n WHERE { ?p <name> ?n }`, QueryOptions{Budget: Budget{MaxRows: 100, MaxBytes: 1000}})
		if err != nil || len(res.Rows) != 2 {
			t.Fatalf("res %v err %v", res, err)
		}
	})
	t.Run("result rows charged", func(t *testing.T) {
		// 2 statements read + 2 rows produced = 4; a budget of 3 trips on output.
		_, err := f.kg.Now().RunQuery(context.Background(), "sparql", `SELECT ?n WHERE { ?p <name> ?n }`, QueryOptions{Budget: Budget{MaxRows: 3}})
		if !errors.Is(err, ErrBudget) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestQueryElemMarkers(t *testing.T) {
	for _, e := range []QElem{QNode{}, QTriple{}, QFilter{}, QOptional{}, QUnion{}, QNotExists{}, QGraph{}} {
		e.qelem()
	}
	// An element type the evaluator does not know is an error, not a panic.
	q := &Query{Vars: []QProj{{Name: "x", Var: "x"}}, Where: []QElem{unknownElem{}}}
	f := newQfix(t)
	_, err := f.kg.Now().Select(context.Background(), q, QueryOptions{})
	if !errors.Is(err, ErrQueryParse) || !strings.Contains(fmt.Sprint(err), "unknownElem") {
		t.Fatalf("err = %v", err)
	}
}

type unknownElem struct{}

func (unknownElem) qelem() {}
