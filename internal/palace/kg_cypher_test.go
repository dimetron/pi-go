package palace

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestCypherRelationshipVarAndLayerProperty(t *testing.T) {
	f := newQfix(t)
	res := f.run(t, "cypher", `MATCH (a)-[r:knows]->(b) WHERE r.confidence > 0.5 RETURN a, b, r, r.confidence AS c`)
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %v", res.Rows)
	}
	r := res.Rows[0]
	if r["a"] != Ent("alice") || r["b"] != Ent("bob") || r["r"] != StmtRef(f.ab) || r["c"] != Num(0.9) {
		t.Fatalf("row = %v", r)
	}
	if !slices.Equal(res.Vars, []string{"a", "b", "r", "c"}) {
		t.Fatalf("vars = %v", res.Vars)
	}
	if !slices.Equal(res.Deps, []int64{f.ab, f.confAB}) {
		t.Fatalf("deps = %v", res.Deps)
	}
	// A relationship without the layer: unknown, rejected at WHERE.
	res = f.run(t, "cypher", `MATCH (a)-[r:knows]->(b) WHERE NOT r.confidence > 0.5 RETURN b`)
	if len(res.Rows) != 0 {
		t.Fatalf("unknown must be rejected: %v", res.Rows)
	}
	// Without a WHERE the unannotated ones show with no value.
	res = f.run(t, "cypher", `MATCH (a)-[r:knows]->(b) RETURN b, r.confidence`)
	wantCol(t, res, "r.confidence", "0.9", "-", "-", "-")
	// Positive-only query is not negative; property accessors do not make it so.
	if res.Negative {
		t.Fatal("accessors must not mark the query negative")
	}
}

func TestCypherPatternsAndProperties(t *testing.T) {
	f := newQfix(t)
	res := f.run(t, "cypher", `MATCH (a {name: 'Alice'})-[:knows]->(b) RETURN b.name AS n ORDER BY n`)
	wantCol(t, res, "n", "-", "Bob")
	res = f.run(t, "cypher", `MATCH (a)-[:knows]->(b)<-[:knows]-(c) WHERE a <> c RETURN a, c`)
	wantCol(t, res, "a", "alice", "bob")
	wantCol(t, res, "c", "bob", "alice")
	// Reverse arrow.
	res = f.run(t, "cypher", `MATCH (b)<-[:knows]-(a {name: 'Alice'}) RETURN b`)
	wantSorted(t, res, "b", "bob", "carol")
	// Undirected.
	res = f.run(t, "cypher", `MATCH (x {name: 'Bob'})-[:knows]-(y) RETURN y`)
	wantSorted(t, res, "y", "alice", "carol")
	// Untyped relationship ignores literal-object statements.
	res = f.run(t, "cypher", `MATCH (a {name: 'Alice'})-[]->(b) RETURN b`)
	wantSorted(t, res, "b", "bob", "carol")
	// Property = literal object only: age is not a relationship.
	res = f.run(t, "cypher", `MATCH (a)-[:age]->(b) RETURN b`)
	if len(res.Rows) != 0 {
		t.Fatal("literal matched as relationship")
	}
	res = f.run(t, "cypher", `MATCH (a)-[:knows]->(b {age: 41}) RETURN a`)
	wantSorted(t, res, "a", "alice", "bob")
	// WHERE with AND / OR / NOT, strings and numbers.
	res = f.run(t, "cypher", `MATCH (p) WHERE (p.age >= 30 AND NOT p.age > 40) OR p.name = 'Bob' RETURN p ORDER BY p`)
	wantCol(t, res, "p", "alice", "bob")
	res = f.run(t, "cypher", `MATCH (p) WHERE p.name CONTAINS 'li' RETURN p`)
	wantCol(t, res, "p", "alice")
	res = f.run(t, "cypher", `MATCH (p) WHERE p.name IS NULL AND p.age IS NOT NULL RETURN p`)
	wantCol(t, res, "p", "carol")
	if !res.Negative {
		t.Fatal("IS NULL is negation")
	}
}

func TestCypherOptionalCountOrderSkipLimit(t *testing.T) {
	f := newQfix(t)
	res := f.run(t, "cypher", `MATCH (p) WHERE p.age > 0 OPTIONAL MATCH (p)-[:knows]->(q) RETURN p, q ORDER BY p DESC`)
	if !res.Negative {
		t.Fatal("OPTIONAL MATCH must mark the query negative")
	}
	wantCol(t, res, "p", "carol", "bob", "alice", "alice")
	wantCol(t, res, "q", "-", "carol", "bob", "carol")
	res = f.run(t, "cypher", `MATCH (p)-[:knows]->(q) RETURN p, count(*) AS n ORDER BY n DESC, p`)
	wantCol(t, res, "p", "alice", "bob", "dave")
	wantCol(t, res, "n", "2", "1", "1")
	res = f.run(t, "cypher", `MATCH (p)-[:knows]->(q) RETURN count(*)`)
	wantCol(t, res, "count(*)", "4")
	res = f.run(t, "cypher", `MATCH (p) WHERE p.age > 0 RETURN p, p.age AS age ORDER BY age SKIP 1 LIMIT 1`)
	wantCol(t, res, "p", "alice")
	res = f.run(t, "cypher", `MATCH (p)-[:knows]->(q) RETURN DISTINCT p ORDER BY p`)
	wantCol(t, res, "p", "alice", "bob", "dave")
	res = f.run(t, "cypher", `MATCH (p {age: 25}) RETURN *`)
	wantCol(t, res, "p", "bob")
}

func TestCypherUnsupportedAndSyntaxErrors(t *testing.T) {
	for _, c := range []struct{ q, construct string }{
		{`CREATE (a)`, "CREATE"},
		{`MATCH (a) SET a.x = 1`, "SET"},
		{`MATCH (a) DELETE a`, "DELETE"},
		{`MATCH (a) DETACH DELETE a`, "DETACH"},
		{`MERGE (a)`, "MERGE"},
		{`MATCH (a)-[:p*1..3]->(b) RETURN b`, "variable-length"},
		{`CALL db.labels()`, "CALL"},
		{`UNWIND [1] AS x RETURN x`, "UNWIND"},
		{`MATCH (a) WITH a RETURN a`, "WITH"},
		{`MATCH (a:Person) RETURN a`, "label"},
		{`MATCH (a)-[:p|q]->(b) RETURN b`, "alternative"},
		{`MATCH (a) MATCH (b) RETURN a`, "multiple MATCH"},
		{`MATCH (a) WHERE a.x + 1 > 2 RETURN a`, "arithmetic"},
		{`MATCH (a) RETURN count(a)`, "count of an expression"},
		{`MATCH (a) RETURN size(a)`, "function"},
	} {
		_, err := ParseCypher(c.q)
		var u *UnsupportedQueryError
		if !errors.Is(err, ErrUnsupportedQuery) || !errors.As(err, &u) {
			t.Errorf("%q: want unsupported, got %v", c.q, err)
			continue
		}
		if !strings.Contains(u.Construct, c.construct) {
			t.Errorf("%q: construct %q, want %q", c.q, u.Construct, c.construct)
		}
		if u.Pos < 0 || u.Pos > len(c.q) || !strings.Contains(err.Error(), "offset") {
			t.Errorf("%q: bad position %d", c.q, u.Pos)
		}
	}
	for _, q := range []string{``, `MATCH`, `MATCH (a RETURN a`, `MATCH (a) RETURN`, `MATCH (a) WHERE b.x = 1 RETURN a`,
		`MATCH (a) RETURN a LIMIT x`, `MATCH (a)-[r:p]-> RETURN a`, `MATCH (a) RETURN 'str`} {
		if _, err := ParseCypher(q); !errors.Is(err, ErrQueryParse) {
			t.Errorf("%q: %v", q, err)
		}
	}
	_, err := ParseCypher("MATCH (a)\nWHERE a.x =\nRETURN a")
	if !errors.Is(err, ErrQueryParse) || !strings.Contains(err.Error(), "3:") {
		t.Fatalf("position in %v", err)
	}
}
