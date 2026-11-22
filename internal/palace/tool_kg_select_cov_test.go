package palace

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestToolKGSelectBranches(t *testing.T) {
	p := newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "alice", Predicate: "works_on", Object: "auth"})
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "bob", Predicate: "works_on", Object: "billing"})
	sel := func(in KGSelectToolInput) string {
		out, err := palaceKGSelectHandler(ctx, p, in)
		if err != nil {
			t.Fatalf("handler error (errors travel in Content): %v", err)
		}
		return out.Content
	}

	mustContain(t, sel(KGSelectToolInput{}), "Error: query is required")
	mustContain(t, sel(KGSelectToolInput{Query: "   "}), "Error: query is required")
	mustContain(t, sel(KGSelectToolInput{Check: "nope"}), "Error:", "nope")
	mustContain(t, sel(KGSelectToolInput{Query: "SELECT ?x WHERE { ?x <works_on> ?y }", TxAsOf: "yesterday-ish"}),
		"Error:", "tx_as_of")
	mustContain(t, sel(KGSelectToolInput{Query: "SELECT ?x WHERE { ?x <works_on> ?y }", AsOf: "not a date"}),
		"Error:", "as_of")
	mustContain(t, sel(KGSelectToolInput{Lang: "gremlin", Query: "g.V()"}), "Error:", "gremlin")
	mustContain(t, sel(KGSelectToolInput{Lang: "CYPHER", Query: "MATCH (a)-[:works_on]->(b) RETURN b ORDER BY b"}),
		"2 row(s)", "| auth |", "| billing |")

	// tx_as_of selects an earlier belief: tx1 only knew about alice.
	out := sel(KGSelectToolInput{Query: "SELECT ?x WHERE { ?x <works_on> ?y }", TxAsOf: "tx1"})
	mustContain(t, out, "1 row(s)", "| alice |")
	if strings.Contains(out, "bob") {
		t.Fatalf("tx1 saw a later fact: %s", out)
	}
	// Facts with an open validity start are valid at any earlier time; the
	// view description carries the bound.
	out = sel(KGSelectToolInput{Query: "SELECT ?x WHERE { ?x <works_on> ?y }", AsOf: "1999-01-01"})
	mustContain(t, out, "2 row(s)", "valid at 1999-01-01")

	// A graph restriction: nothing is in the graph yet.
	out = sel(KGSelectToolInput{Query: "SELECT ?x WHERE { ?x <works_on> ?y }", Graph: "crm"})
	mustContain(t, out, "0 row(s)", "(no rows)", "crm")

	// max_rows: the row cap applies to the result, not only the budget.
	mustContain(t, sel(KGSelectToolInput{Query: "SELECT ?x WHERE { ?x <works_on> ?y }", MaxRows: 1}), "Error:", "max_rows=1")
	// A negative max_rows falls back to the default.
	mustContain(t, sel(KGSelectToolInput{Query: "SELECT ?x WHERE { ?x <works_on> ?y }", MaxRows: -5}), "2 row(s)")

	// save_as with a blank name is reported, not saved.
	mustContain(t, sel(KGSelectToolInput{Query: "SELECT ?x WHERE { ?x <works_on> ?y }", SaveAs: "   "}), "Error:", "needs a name")
	// Saving then checking reports the checkpoint and rows.
	mustContain(t, sel(KGSelectToolInput{Query: "SELECT ?x WHERE { ?x <works_on> <auth> }", SaveAs: "auth"}), "saved as auth", "| alice |")
	mustContain(t, sel(KGSelectToolInput{Check: "auth"}), "still fresh", "1 rows", "| alice |")
	// A cypher saved answer is checkable through the tool too.
	mustContain(t, sel(KGSelectToolInput{Lang: "cypher", Query: "MATCH (a)-[:works_on]->(b) RETURN a, b", SaveAs: "cy"}), "saved as cy")
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "carol", Predicate: "works_on", Object: "auth"})
	mustContain(t, sel(KGSelectToolInput{Check: "cy"}), "is stale", "Run the query again")
}

func TestFormatRowsShapes(t *testing.T) {
	if got := FormatRows([]string{"a"}, nil); got != "(no rows)" {
		t.Fatalf("empty = %q", got)
	}
	rows := []map[string]Value{{"b": Int(2), "a": Str("x")}, {"a": Ent("e")}}
	// Without vars the columns are the sorted union of the keys.
	got := FormatRows(nil, rows)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 4 || lines[0] != "| a | b |" || lines[1] != "|---|---|" {
		t.Fatalf("table = %q", got)
	}
	if !slices.Equal(lines[2:], []string{"| x | 2 |", "| e |  |"}) {
		t.Fatalf("rows = %q", lines[2:])
	}
	// With vars the given order wins and missing cells are blank.
	got = FormatRows([]string{"b", "a"}, rows)
	if !strings.HasPrefix(got, "| b | a |\n|---|---|\n| 2 | x |\n|  | e |") {
		t.Fatalf("ordered = %q", got)
	}
}
