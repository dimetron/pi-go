package palace

import (
	"context"
	"testing"
)

func TestToolKG_Select(t *testing.T) {
	p := newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	conf := 0.9
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "alice", Predicate: "works_on", Object: "auth", Confidence: &conf})
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "bob", Predicate: "works_on", Object: "billing"})

	out, _ := palaceKGSelectHandler(ctx, p, KGSelectToolInput{Query: "SELECT ?who WHERE { ?who <works_on> <auth> }"})
	mustContain(t, out.Content, "1 row(s), now", "| who |", "| alice |")
	out, _ = palaceKGSelectHandler(ctx, p, KGSelectToolInput{Lang: "cypher",
		Query: "MATCH (a)-[r:works_on]->(b) WHERE r.confidence > 0.5 RETURN a, b"})
	mustContain(t, out.Content, "1 row(s)", "| alice | auth |")
	out, _ = palaceKGSelectHandler(ctx, p, KGSelectToolInput{Query: "SELECT ?a ?b WHERE { ?a <works_on> ?b }", MaxRows: 1})
	mustContain(t, out.Content, "Error:")
	out, _ = palaceKGSelectHandler(ctx, p, KGSelectToolInput{Query: "CONSTRUCT { ?a ?b ?c } WHERE { ?a ?b ?c }"})
	mustContain(t, out.Content, "Error:")

	out, _ = palaceKGSelectHandler(ctx, p, KGSelectToolInput{Query: "SELECT ?who WHERE { ?who <works_on> ?x }", SaveAs: "staff"})
	mustContain(t, out.Content, "2 row(s)", "saved as staff")
	out, _ = palaceKGSelectHandler(ctx, p, KGSelectToolInput{Check: "staff"})
	mustContain(t, out.Content, "still fresh")
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "carol", Predicate: "works_on", Object: "auth"})
	out, _ = palaceKGSelectHandler(ctx, p, KGSelectToolInput{Check: "staff"})
	mustContain(t, out.Content, "is stale", "may add rows")
}
