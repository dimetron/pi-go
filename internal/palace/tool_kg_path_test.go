package palace

import (
	"context"
	"testing"
)

func TestToolKG_Path(t *testing.T) {
	p := newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	for _, in := range []KGAddToolInput{
		{Subject: "Bob", Predicate: "owns", Object: "billing", Graph: "team"},
		{Subject: "billing", Predicate: "depends_on", Object: "ledger"},
	} {
		if out, _ := palaceKGAddHandler(ctx, p, in); out.Content == "" {
			t.Fatal("add failed")
		}
	}
	out, _ := palaceKGPathHandler(ctx, p, KGPathToolInput{From: "bob", To: "ledger"})
	mustContain(t, out.Content, "1 path(s)", "bob -[e1]-> billing -[e3]-> ledger")
	out, _ = palaceKGPathHandler(ctx, p, KGPathToolInput{From: "bob", To: "ledger", Graph: "team"})
	mustContain(t, out.Content, "No path found.")
	out, _ = palaceKGPathHandler(ctx, p, KGPathToolInput{})
	mustContain(t, out.Content, "Error: from or to is required")
}
