package palace

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSPARQLJoinAndAbbreviations(t *testing.T) {
	f := newQfix(t)
	res := f.run(t, "sparql", `PREFIX ex: <http://ex.org/>
SELECT ?a ?c WHERE { ?a ex:knows ?b . ?b ex:knows ?c }`)
	wantCol(t, res, "a", "alice", "dave", "dave")
	wantCol(t, res, "c", "carol", "bob", "carol")
	if !slices.Equal(res.Vars, []string{"a", "c"}) {
		t.Fatalf("vars = %v", res.Vars)
	}
	// Deterministic by the eids matched; Deps cover each row's statements.
	if !slices.Equal(res.Deps, []int64{f.ab, f.ac, f.bc, f.da}) {
		t.Fatalf("deps = %v", res.Deps)
	}
	if len(res.Preds) != 1 || res.Preds[0] != Ent("knows") || res.Negative {
		t.Fatalf("preds %v negative %v", res.Preds, res.Negative)
	}

	// ; and , abbreviations, prefixed and IRI terms agree.
	res = f.run(t, "sparql", `PREFIX ex: <http://ex.org/>
SELECT ?x WHERE { ?x ex:knows ex:bob, <http://ex.org/carol> ; ex:age 30 . }`)
	wantCol(t, res, "x", "alice")
	// SELECT * follows first appearance.
	res = f.run(t, "sparql", `SELECT * WHERE { ?p <name> ?n }`)
	if !slices.Equal(res.Vars, []string{"p", "n"}) || len(res.Rows) != 2 {
		t.Fatalf("select *: %v %d", res.Vars, len(res.Rows))
	}
	// Literals: decimals, booleans, strings.
	assert1(t, f.kg, Ent("alice"), Ent("score"), Num(1.5), Valid{})
	assert1(t, f.kg, Ent("alice"), Ent("active"), Bool(true), Valid{})
	res = f.run(t, "sparql", `SELECT ?p WHERE { ?p <score> 1.5 ; <active> true ; <name> "Alice" }`)
	wantCol(t, res, "p", "alice")
	// A literal in a different type does not match.
	if res = f.run(t, "sparql", `SELECT ?p WHERE { ?p <age> "30" }`); len(res.Rows) != 0 {
		t.Fatal("string matched an integer")
	}
}

func TestSPARQLOptionalUnionNotExists(t *testing.T) {
	f := newQfix(t)
	res := f.run(t, "sparql", `SELECT ?p ?n WHERE { ?p <age> ?x . OPTIONAL { ?p <name> ?n } }`)
	wantCol(t, res, "p", "alice", "bob", "carol")
	wantCol(t, res, "n", "Alice", "Bob", "-")
	if !res.Negative {
		t.Fatal("OPTIONAL must mark the query negative")
	}

	res = f.run(t, "sparql", `SELECT ?x WHERE { { ?x <age> 30 } UNION { ?x <age> 41 } UNION { <dave> <knows> ?x } }`)
	wantSorted(t, res, "x", "alice", "carol", "alice")

	res = f.run(t, "sparql", `SELECT ?p WHERE { ?p <age> ?x . FILTER NOT EXISTS { ?p <knows> ?q } }`)
	wantCol(t, res, "p", "carol")
	if !res.Negative {
		t.Fatal("NOT EXISTS must mark the query negative")
	}
	if res.Preds == nil || len(res.Preds) != 2 {
		t.Fatalf("preds should include the negated pattern: %v", res.Preds)
	}
	// Filters apply to the whole group wherever they are written.
	res = f.run(t, "sparql", `SELECT ?p WHERE { FILTER(?n > 28) ?p <age> ?n }`)
	wantSorted(t, res, "p", "alice", "carol")
	res = f.run(t, "sparql", `SELECT ?p WHERE { ?p <name> ?n . FILTER(regex(?n, "^a", "i")) }`)
	wantCol(t, res, "p", "alice")
	res = f.run(t, "sparql", `SELECT ?p WHERE { ?p <name> ?n . FILTER(contains(?n, "ob") && strstarts(?n, "B")) }`)
	wantCol(t, res, "p", "bob")
}

func TestSPARQLOrderLimitOffsetCount(t *testing.T) {
	f := newQfix(t)
	res := f.run(t, "sparql", `SELECT ?p ?n WHERE { ?p <age> ?n } ORDER BY DESC(?n)`)
	wantCol(t, res, "p", "carol", "alice", "bob")
	res = f.run(t, "sparql", `SELECT ?p WHERE { ?p <age> ?n } ORDER BY ?n LIMIT 1 OFFSET 1`)
	wantCol(t, res, "p", "alice")
	if !slices.Equal(res.Deps, []int64{f.ageA}) {
		t.Fatalf("deps follow the rows kept: %v", res.Deps)
	}
	res = f.run(t, "sparql", `SELECT (COUNT(*) AS ?n) WHERE { ?a <knows> ?b }`)
	if len(res.Rows) != 1 || res.Rows[0]["n"] != Int(4) || !res.Negative || len(res.Deps) != 4 {
		t.Fatalf("count: %+v", res)
	}
	res = f.run(t, "sparql", `SELECT (COUNT(*) AS ?n) WHERE { ?a <nothing> ?b }`)
	if len(res.Rows) != 1 || res.Rows[0]["n"] != Int(0) {
		t.Fatalf("count over nothing: %+v", res.Rows)
	}
	res = f.run(t, "sparql", `SELECT DISTINCT ?a WHERE { ?a <knows> ?b }`)
	wantCol(t, res, "a", "alice", "bob", "dave")
	res = f.run(t, "sparql", `SELECT ?a WHERE { ?a <knows> ?b } LIMIT 0`)
	if len(res.Rows) != 0 {
		t.Fatal("LIMIT 0")
	}
}

func TestSPARQLGraph(t *testing.T) {
	f := newQfix(t)
	tx(t, f.kg, func(w *KGTx) error {
		_, _, err := w.AddToGraph(f.ab, "work")
		return err
	})
	res := f.run(t, "sparql", `SELECT ?b WHERE { GRAPH <http://ex/work> { <alice> <knows> ?b } }`)
	wantCol(t, res, "b", "bob")
	// Outside GRAPH the default graph is the union.
	res = f.run(t, "sparql", `SELECT ?b WHERE { <alice> <knows> ?b }`)
	wantSorted(t, res, "b", "bob", "carol")
	// The view's own graph scope is respected too.
	got, err := f.kg.Now().InGraph("work").RunQuery(context.Background(), "sparql", `SELECT ?b WHERE { <alice> <knows> ?b }`, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantCol(t, got, "b", "bob")
	res = f.run(t, "sparql", `SELECT ?b WHERE { GRAPH <nowhere> { <alice> <knows> ?b } }`)
	if len(res.Rows) != 0 {
		t.Fatal("unknown graph matched")
	}
}

func TestSPARQLRDFStarSubject(t *testing.T) {
	f := newQfix(t)
	res := f.run(t, "sparql", `SELECT ?c WHERE { << <alice> <knows> <bob> >> <confidence> ?c }`)
	if len(res.Rows) != 1 || res.Rows[0]["c"] != Num(0.9) {
		t.Fatalf("rdf-star: %v", res.Rows)
	}
	if !slices.Equal(res.Deps, []int64{f.ab, f.confAB}) {
		t.Fatalf("deps = %v", res.Deps)
	}
	// The statement itself can be selected through a variable object triple.
	res = f.run(t, "sparql", `SELECT ?x ?c WHERE { << ?x <knows> ?y >> <confidence> ?c }`)
	wantCol(t, res, "x", "alice")
	// Nothing annotates alice->carol.
	res = f.run(t, "sparql", `SELECT ?c WHERE { << <alice> <knows> <carol> >> <confidence> ?c }`)
	if len(res.Rows) != 0 {
		t.Fatal("unannotated statement matched")
	}
}

func TestSPARQLUnsupportedAndSyntaxErrors(t *testing.T) {
	for _, c := range []struct{ q, construct string }{
		{`CONSTRUCT { ?s ?p ?o } WHERE { ?s ?p ?o }`, "CONSTRUCT"},
		{`ASK { ?s ?p ?o }`, "ASK"},
		{`SELECT ?s WHERE { ?s <a>/<b> ?o }`, "property path"},
		{`SELECT ?s WHERE { ?s <a>+ ?o }`, "property path"},
		{`SELECT ?s WHERE { ?s <a> ?o . BIND(1 AS ?x) }`, "BIND"},
		{`SELECT ?s WHERE { VALUES ?s { <a> } }`, "VALUES"},
		{`SELECT (SUM(?o) AS ?n) WHERE { ?s <a> ?o }`, "aggregate"},
		{`SELECT (COUNT(?o) AS ?n) WHERE { ?s <a> ?o }`, "COUNT"},
		{`SELECT ?s WHERE { { SELECT ?s WHERE { ?s <a> ?o } } }`, "subquery"},
		{`SELECT ?s WHERE { ?s <a> ?o } GROUP BY ?s`, "GROUP"},
		{`SELECT ?s WHERE { ?s a <T> }`, "rdf:type"},
		{`SELECT ?s WHERE { ?s <a> ?o . FILTER(?o + 1 > 2) }`, "arithmetic"},
		{`SELECT ?s WHERE { ?s <a> ?o . FILTER(strlen(?o) > 2) }`, "function"},
		{`SELECT ?s WHERE { ?s <a> _:b }`, "blank"},
		{`SELECT ?s WHERE { GRAPH ?g { ?s <a> ?o } }`, "GRAPH"},
	} {
		_, err := ParseSPARQL(c.q)
		var u *UnsupportedQueryError
		if !errors.Is(err, ErrUnsupportedQuery) || !errors.As(err, &u) {
			t.Errorf("%q: want unsupported, got %v", c.q, err)
			continue
		}
		if !strings.Contains(u.Construct, c.construct) {
			t.Errorf("%q: construct %q, want %q", c.q, u.Construct, c.construct)
		}
		if u.Pos < 0 || u.Pos > len(c.q) || !strings.Contains(err.Error(), "offset") {
			t.Errorf("%q: bad position %d (%v)", c.q, u.Pos, err)
		}
	}
	if _, err := ParseSPARQL("SELECT ?x WHERE { ?x <a> ?y . FILTER(?y > ) }"); !errors.Is(err, ErrQueryParse) || errors.Is(err, ErrUnsupportedQuery) {
		t.Fatalf("syntax error = %v", err)
	}
	// Position is line:col.
	_, err := ParseSPARQL("SELECT ?x\nWHERE { ?x <a> ?y\n  FILTER( }")
	if !errors.Is(err, ErrQueryParse) || !strings.Contains(err.Error(), "3:") {
		t.Fatalf("position in %v", err)
	}
	for _, q := range []string{``, `SELECT`, `SELECT ?x WHERE {`, `SELECT ?x WHERE { ?x <a> "unterminated }`, `SELECT ?x WHERE { ?x <a> ?y } LIMIT x`, `SELECT ?x WHERE { ?x <a> ?y } trailing`} {
		if _, err := ParseSPARQL(q); !errors.Is(err, ErrQueryParse) {
			t.Errorf("%q: %v", q, err)
		}
	}
}
