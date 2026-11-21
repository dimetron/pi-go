package palace

import "testing"

func TestCypherErrors(t *testing.T) {
	runErrCases(t, "cypher", []covErrCase{
		{name: "empty", q: ``, at: "end of query"},
		{name: "create", q: `CREATE (a)`, unsupported: true, at: "offset 0"},
		{name: "merge-lower", q: `merge (a)`, unsupported: true},
		{name: "not-match", q: `RETURN 1`, at: "expected MATCH"},
		{name: "number-first", q: `5`, at: "1:1"},
		{name: "unterminated-string", q: "MATCH (a {name: 'x}) RETURN a", at: "1:17"},
		{name: "unterminated-ident", q: "MATCH (a) RETURN `a", at: "1:18"},
		{name: "bad-char", q: `MATCH (a) RETURN a @`, at: "unexpected character"},
		{name: "second-match", q: `MATCH (a) MATCH (b) RETURN a`, unsupported: true},
		{name: "set-after", q: `MATCH (a) SET a.x = 1 RETURN a`, unsupported: true},
		{name: "optional-no-match", q: `MATCH (a) OPTIONAL (b) RETURN a`, at: "expected MATCH after OPTIONAL"},
		{name: "optional-bad-body", q: `MATCH (a) OPTIONAL MATCH a RETURN a`},
		{name: "junk-between", q: `MATCH (a) FOO RETURN a`, at: "expected OPTIONAL MATCH or RETURN"},
		{name: "no-return", q: `MATCH (a)`, at: "end of query"},
		{name: "where-bad", q: `MATCH (a) WHERE RETURN a`},
		{name: "node-missing-paren", q: `MATCH a RETURN a`, at: `expected "("`},
		{name: "node-label", q: `MATCH (a:Person) RETURN a`, unsupported: true},
		{name: "prop-key-not-ident", q: `MATCH (a {1: 2}) RETURN a`, at: "property name"},
		{name: "prop-no-colon", q: `MATCH (a {name 'x'}) RETURN a`},
		{name: "prop-bad-literal", q: `MATCH (a {name: foo}) RETURN a`, at: "expected a literal"},
		{name: "prop-list", q: `MATCH (a {name: [1]}) RETURN a`, unsupported: true},
		{name: "prop-map", q: `MATCH (a {name: {x: 1}}) RETURN a`, unsupported: true},
		{name: "prop-neg-string", q: `MATCH (a {name: -'x'}) RETURN a`, at: "cannot negate"},
		{name: "prop-int-overflow", q: `MATCH (a {n: 99999999999999999999}) RETURN a`, at: "integer out of range"},
		{name: "prop-unclosed", q: `MATCH (a {name: 'x') RETURN a`},
		{name: "node-unclosed", q: `MATCH (a RETURN a`},
		{name: "rel-type-not-ident", q: `MATCH (a)-[:5]->(b) RETURN a`, at: "relationship type"},
		{name: "rel-alt-types", q: `MATCH (a)-[:x|y]->(b) RETURN a`, unsupported: true},
		{name: "rel-varlen", q: `MATCH (a)-[:x*]->(b) RETURN a`, unsupported: true},
		{name: "rel-varlen-untyped", q: `MATCH (a)-[*]->(b) RETURN a`, unsupported: true},
		{name: "rel-props", q: `MATCH (a)-[:x {w: 1}]->(b) RETURN a`, unsupported: true},
		{name: "rel-unclosed", q: `MATCH (a)-[:x->(b) RETURN a`},
		{name: "rel-bad-close", q: `MATCH (a)-[:x]>(b) RETURN a`, at: "to close the relationship"},
		{name: "rel-bidirectional", q: `MATCH (a)<-[:x]->(b) RETURN a`, unsupported: true},
		{name: "rel-right-node-broken", q: `MATCH (a)-[:x]->b RETURN a`},
		{name: "unknown-var-where", q: `MATCH (a) WHERE z.name = 'x' RETURN a`, at: `unknown variable "z"`},
		{name: "unknown-bare-var-where", q: `MATCH (a) WHERE z = 'x' RETURN a`, at: `unknown variable "z"`},
		{name: "function-in-where", q: `MATCH (a) WHERE toLower(a.name) = 'x' RETURN a`, unsupported: true},
		{name: "prop-after-dot", q: `MATCH (a) WHERE a.5 = 1 RETURN a`, at: "property name after"},
		{name: "regex-match", q: `MATCH (a) WHERE a.name =~ 'x.*' RETURN a`, unsupported: true},
		{name: "arith-plus", q: `MATCH (a) WHERE a.n + 1 = 2 RETURN a`, unsupported: true},
		{name: "arith-minus", q: `MATCH (a) WHERE a.n - 1 = 2 RETURN a`, unsupported: true},
		{name: "arith-mod", q: `MATCH (a) WHERE a.n % 2 = 0 RETURN a`, unsupported: true},
		{name: "arith-pow", q: `MATCH (a) WHERE a.n ^ 2 = 0 RETURN a`, unsupported: true},
		{name: "is-no-null", q: `MATCH (a) WHERE a.n IS 3 RETURN a`, at: "expected NULL"},
		{name: "is-null-expr", q: `MATCH (a) WHERE 3 IS NULL RETURN a`, unsupported: true},
		{name: "starts-no-with", q: `MATCH (a) WHERE a.n STARTS 'x' RETURN a`, at: "expected WITH"},
		{name: "in-list", q: `MATCH (a) WHERE a.n IN [1] RETURN a`, unsupported: true},
		{name: "contains-broken", q: `MATCH (a) WHERE a.n CONTAINS RETURN a`},
		{name: "starts-with-broken", q: `MATCH (a) WHERE a.n STARTS WITH RETURN a`},
		{name: "cmp-rhs-broken", q: `MATCH (a) WHERE a.n = RETURN a`},
		{name: "and-broken", q: `MATCH (a) WHERE a.n = 1 AND RETURN a`},
		{name: "or-broken", q: `MATCH (a) WHERE a.n = 1 OR RETURN a`},
		{name: "not-broken", q: `MATCH (a) WHERE NOT RETURN a`},
		{name: "paren-unclosed", q: `MATCH (a) WHERE (a.n = 1 RETURN a`},
		{name: "paren-inner-broken", q: `MATCH (a) WHERE () RETURN a`, at: "expected an expression"},
		{name: "return-number", q: `MATCH (a) RETURN 5`, at: "expected a variable or property"},
		{name: "return-expr", q: `MATCH (a) RETURN a = 1`, at: "after RETURN"},
		{name: "return-function", q: `MATCH (a) RETURN toLower(a.name)`, unsupported: true},
		{name: "return-const-ident", q: `MATCH (a) RETURN true`, unsupported: true},
		{name: "return-unknown-var", q: `MATCH (a) RETURN b`, at: `unknown variable "b"`},
		{name: "return-alias-not-ident", q: `MATCH (a) RETURN a AS 5`, at: "alias"},
		{name: "count-distinct", q: `MATCH (a) RETURN count(DISTINCT a)`, unsupported: true},
		{name: "count-expr", q: `MATCH (a) RETURN count(a)`, unsupported: true},
		{name: "count-unclosed", q: `MATCH (a) RETURN count(*`},
		{name: "count-alias-bad", q: `MATCH (a) RETURN count(*) AS 5`, at: "alias"},
		{name: "two-counts", q: `MATCH (a) RETURN count(*) AS x, count(*) AS y`, unsupported: true},
		{name: "order-no-by", q: `MATCH (a) RETURN a ORDER a`, at: "expected BY"},
		{name: "order-expr", q: `MATCH (a) RETURN a ORDER BY 5`, unsupported: true},
		{name: "order-bad", q: `MATCH (a) RETURN a ORDER BY ,`, at: "expected an expression"},
		{name: "skip-word", q: `MATCH (a) RETURN a SKIP x`, at: "SKIP takes an integer"},
		{name: "limit-word", q: `MATCH (a) RETURN a LIMIT x`, at: "LIMIT takes an integer"},
		{name: "trailing-junk", q: `MATCH (a) RETURN a foo`, at: "after RETURN"},
		{name: "trailing-union", q: `MATCH (a) RETURN a UNION MATCH (b) RETURN b`, unsupported: true},
		{name: "trailing-with-semicolon-junk", q: `MATCH (a) RETURN a; MATCH (b) RETURN b`, at: "after RETURN"},
		{name: "return-star-then-junk", q: `MATCH (a) RETURN * x`, at: "after RETURN"},
		{name: "quoted-keyword-is-ident", q: "MATCH (a) `RETURN` a", at: "expected OPTIONAL MATCH or RETURN"},
	})
}

func TestCypherLiteralsAndLexing(t *testing.T) {
	f := newQfix(t)
	assert1(t, f.kg, Ent("alice"), Ent("score"), Num(-2.5), Valid{})
	assert1(t, f.kg, Ent("alice"), Ent("neg"), Int(-7), Valid{})
	assert1(t, f.kg, Ent("alice"), Ent("flag"), Bool(true), Valid{})
	assert1(t, f.kg, Ent("alice"), Ent("off"), Bool(false), Valid{})
	assert1(t, f.kg, Ent("alice"), Ent("note"), Str("a\nb\tc'd"), Valid{})
	assert1(t, f.kg, Ent("dave"), Ent("note"), Str("x y"), Valid{})
	assert1(t, f.kg, Ent("dave"), Ent("odd name"), Str("q"), Valid{})

	cases := []struct {
		name, q, col string
		want         []string
	}{
		{"negative decimal property", `MATCH (a {score: -2.5}) RETURN a`, "a", []string{"alice"}},
		{"negative int property", `MATCH (a {neg: -7}) RETURN a`, "a", []string{"alice"}},
		{"true property", `MATCH (a {flag: true}) RETURN a`, "a", []string{"alice"}},
		{"false property", `MATCH (a {off: FALSE}) RETURN a`, "a", []string{"alice"}},
		{"escaped string", `MATCH (a {note: "a\nb\tc'd"}) RETURN a`, "a", []string{"alice"}},
		{"multiple props", `MATCH (a {flag: true, neg: -7}) RETURN a`, "a", []string{"alice"}},
		{"trailing comma in map", `MATCH (a {flag: true,}) RETURN a`, "a", []string{"alice"}},
		{"line comment", "MATCH (a {flag: true}) // comment\nRETURN a", "a", []string{"alice"}},
		{"lowercase keywords", `match (a {flag: true}) return a`, "a", []string{"alice"}},
		{"trailing semicolon", `MATCH (a {flag: true}) RETURN a;`, "a", []string{"alice"}},
		{"quoted identifier", "MATCH (`x y` {flag: true}) RETURN `x y`", "x y", []string{"alice"}},
		{"quoted property", "MATCH (a) WHERE a.`odd name` = 'q' RETURN a", "a", []string{"dave"}},
		{"where bool const", `MATCH (a {flag: true}) WHERE true RETURN a`, "a", []string{"alice"}},
		{"where false const", `MATCH (a {flag: true}) WHERE FALSE RETURN a`, "a", nil},
		{"where negative literal", `MATCH (a)-[:age]->(b) RETURN a`, "a", nil},
		{"where lt negative", `MATCH (a {flag: true}) WHERE a.neg < -3 RETURN a`, "a", []string{"alice"}},
		{"where decimal", `MATCH (a {flag: true}) WHERE a.score <= -2.5 RETURN a`, "a", []string{"alice"}},
		{"where ge", `MATCH (a {flag: true}) WHERE a.neg >= -7 RETURN a`, "a", []string{"alice"}},
		{"where ne bang", `MATCH (a {flag: true}) WHERE a.neg != -7 RETURN a`, "a", nil},
		{"where ne angle", `MATCH (a {flag: true}) WHERE a.neg <> 0 RETURN a`, "a", []string{"alice"}},
		{"where or", `MATCH (a) WHERE a.flag = true OR a.note = 'x y' RETURN a`, "a", []string{"alice", "dave"}},
		{"where and", `MATCH (a) WHERE a.flag = true AND a.neg < 0 RETURN a`, "a", []string{"alice"}},
		{"where not", `MATCH (a {flag: true}) WHERE NOT a.neg = 0 RETURN a`, "a", []string{"alice"}},
		{"where double not", `MATCH (a {flag: true}) WHERE NOT NOT a.neg = -7 RETURN a`, "a", []string{"alice"}},
		{"where parens", `MATCH (a) WHERE (a.flag = true OR a.note = 'x y') AND a.name = 'Alice' RETURN a`, "a", []string{"alice"}},
		{"is null", `MATCH (a) WHERE a.score IS NULL RETURN a`, "a", []string{"bob", "carol", "dave"}},
		{"is not null", `MATCH (a) WHERE a.score IS NOT NULL RETURN a`, "a", []string{"alice"}},
		{"contains", `MATCH (a) WHERE a.note CONTAINS 'y' RETURN a`, "a", []string{"dave"}},
		{"starts with", `MATCH (a) WHERE a.note STARTS WITH 'x' RETURN a`, "a", []string{"dave"}},
		{"var compare", `MATCH (a)-[:knows]->(b) WHERE a = b RETURN a`, "a", nil},
		{"entity ne var", `MATCH (a)-[:knows]->(b) WHERE a <> b RETURN a`, "a", []string{"alice", "alice", "bob", "dave"}},
		{"return star", `MATCH (a {flag: true}) RETURN *`, "a", []string{"alice"}},
		{"return distinct", `MATCH (a)-[:knows]->(b) RETURN DISTINCT a`, "a", []string{"alice", "bob", "dave"}},
		{"alias", `MATCH (a {flag: true}) RETURN a AS who`, "who", []string{"alice"}},
		{"prop alias", `MATCH (a {flag: true}) RETURN a.neg AS n`, "n", []string{"-7"}},
		{"prop without alias", `MATCH (a {flag: true}) RETURN a.neg`, "a.neg", []string{"-7"}},
		{"count alias", `MATCH (a)-[:knows]->(b) RETURN count(*) AS n`, "n", []string{"4"}},
		{"count default name", `MATCH (a)-[:knows]->(b) RETURN COUNT(*)`, "count(*)", []string{"4"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := f.run(t, "cypher", c.q)
			wantSorted(t, res, c.col, c.want...)
		})
	}
}

func TestCypherReturnOrderingAndPaging(t *testing.T) {
	f := newQfix(t)
	cases := []struct {
		name, q, col string
		want         []string
	}{
		{"order alias asc", `MATCH (p)-[:age]->(n) RETURN p`, "p", nil},
		{"order by projected name", `MATCH (a {name: 'Alice'}) RETURN a, a.age AS n ORDER BY n`, "a", []string{"alice"}},
		{"order by property key", `MATCH (a) WHERE a.age IS NOT NULL RETURN a ORDER BY a.age`, "a", []string{"bob", "alice", "carol"}},
		{"order by property desc", `MATCH (a) WHERE a.age IS NOT NULL RETURN a ORDER BY a.age DESC`, "a", []string{"carol", "alice", "bob"}},
		{"order descending keyword", `MATCH (a) WHERE a.age IS NOT NULL RETURN a ORDER BY a.age DESCENDING`, "a", []string{"carol", "alice", "bob"}},
		{"order ascending keyword", `MATCH (a) WHERE a.age IS NOT NULL RETURN a ORDER BY a.age ASCENDING`, "a", []string{"bob", "alice", "carol"}},
		{"order asc keyword", `MATCH (a) WHERE a.age IS NOT NULL RETURN a ORDER BY a.age ASC`, "a", []string{"bob", "alice", "carol"}},
		{"order two keys", `MATCH (a)-[:knows]->(b) RETURN a, b ORDER BY a DESC, b`, "b", []string{"alice", "carol", "bob", "carol"}},
		{"skip", `MATCH (a) WHERE a.age IS NOT NULL RETURN a ORDER BY a.age SKIP 1`, "a", []string{"alice", "carol"}},
		{"limit", `MATCH (a) WHERE a.age IS NOT NULL RETURN a ORDER BY a.age LIMIT 2`, "a", []string{"bob", "alice"}},
		{"skip limit", `MATCH (a) WHERE a.age IS NOT NULL RETURN a ORDER BY a.age SKIP 1 LIMIT 1`, "a", []string{"alice"}},
		{"limit skip", `MATCH (a) WHERE a.age IS NOT NULL RETURN a ORDER BY a.age LIMIT 5 SKIP 2`, "a", []string{"carol"}},
		{"limit zero", `MATCH (a)-[:knows]->(b) RETURN a LIMIT 0`, "a", nil},
		{"skip past end", `MATCH (a)-[:knows]->(b) RETURN a SKIP 99`, "a", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := f.run(t, "cypher", c.q)
			if c.name == "order two keys" || c.name == "order by projected name" || c.name == "order alias asc" {
				wantSorted(t, res, c.col, c.want...)
				return
			}
			wantCol(t, res, c.col, c.want...)
		})
	}
	res := f.run(t, "cypher", `MATCH (a)-[:knows]->(b) RETURN a, b ORDER BY a DESC, b`)
	wantCol(t, res, "a", "dave", "bob", "alice", "alice")
	wantCol(t, res, "b", "alice", "carol", "bob", "carol")
	if res.Negative {
		t.Fatal("a plain pattern is monotonic")
	}
}

func TestCypherNodePatternsAndOptional(t *testing.T) {
	f := newQfix(t)
	// A bare node pattern scans the entities.
	res := f.run(t, "cypher", `MATCH (a) RETURN a`)
	wantSorted(t, res, "a", "alice", "bob", "carol", "dave")
	// An anonymous bare node does not bind anything visible.
	res = f.run(t, "cypher", `MATCH () RETURN *`)
	if len(res.Vars) != 0 {
		t.Fatalf("vars = %v", res.Vars)
	}
	// A node that is a literal-bound property is not an entity.
	res = f.run(t, "cypher", `MATCH (a)-[:knows]->(b), (b) RETURN b`)
	wantSorted(t, res, "b", "alice", "bob", "carol", "carol")
	// OPTIONAL MATCH on an unbound variable and a property of it.
	res = f.run(t, "cypher", `MATCH (a) OPTIONAL MATCH (a)-[:knows]->(b) RETURN a, b.name`)
	wantSorted(t, res, "a", "alice", "alice", "bob", "carol", "dave")
	wantSorted(t, res, "b.name", "-", "-", "-", "Alice", "Bob")
	if !res.Negative {
		t.Fatal("OPTIONAL MATCH is negative")
	}
	// A repeated relationship variable names the same statement.
	res = f.run(t, "cypher", `MATCH (a)-[r:knows]->(b), (c)-[r]->(d) RETURN a, c`)
	wantCol(t, res, "a", "alice", "alice", "bob", "dave")
	wantCol(t, res, "c", "alice", "alice", "bob", "dave")
	// A relationship variable bound to an entity does not match.
	res = f.run(t, "cypher", `MATCH (a)-[:knows]->(r), (c)-[r:knows]->(d) RETURN a`)
	if len(res.Rows) != 0 {
		t.Fatalf("rows = %v", res.Rows)
	}
	// Relationship isomorphism: the same statement cannot serve two hops.
	res = f.run(t, "cypher", `MATCH (a)-[:knows]-(b)-[:knows]-(c) WHERE a = c RETURN a`)
	if len(res.Rows) != 0 {
		t.Fatalf("one statement used twice: %v", res.Rows)
	}
	// A repeated node variable in a path must agree (self loop).
	res = f.run(t, "cypher", `MATCH (a)-[:knows]->(a) RETURN a`)
	if len(res.Rows) != 0 {
		t.Fatalf("self-loop rows = %v", res.Rows)
	}
	// Property accessor on an optional-bound variable of an earlier scope.
	res = f.run(t, "cypher", `MATCH (a {name: 'Alice'}) OPTIONAL MATCH (a)-[:knows]->(b) WHERE b.name = 'Bob' RETURN a, b`)
	wantCol(t, res, "b", "bob")
	// The same accessor used twice shares one lookup.
	res = f.run(t, "cypher", `MATCH (a) WHERE a.age > 20 AND a.age < 35 RETURN a, a.age`)
	wantCol(t, res, "a", "alice", "bob")
	// Dependencies include the statements read by accessors.
	res = f.run(t, "cypher", `MATCH (a {name: 'Alice'}) RETURN a.age`)
	if len(res.Deps) != 2 {
		t.Fatalf("deps = %v", res.Deps)
	}
}

func TestCypherBudgetsAndCancellation(t *testing.T) {
	f := newQfix(t)
	for _, q := range []string{
		`MATCH (a)-[:knows]->(b) RETURN a, b`,
		`MATCH (a) RETURN a`,
		`MATCH (a) OPTIONAL MATCH (a)-[:knows]->(b) RETURN a, b`,
		`MATCH (a)-[:knows]-(b) WHERE NOT a = b RETURN a`,
	} {
		_, err := covRunBudget(f, "cypher", q, Budget{MaxRows: 2})
		if !isBudget(err) {
			t.Fatalf("%q: err = %v", q, err)
		}
	}
}
