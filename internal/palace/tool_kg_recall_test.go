package palace

import (
	"context"
	"strings"
	"testing"
)

func TestToolKG_RecallTextFallbackAndHybrid(t *testing.T) {
	p := newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "service", Predicate: "stores", Object: "invoices", Graph: "billing"})
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "cache", Predicate: "holds", Object: "sessions"})

	out, _ := palaceKGRecallHandler(ctx, p, KGRecallToolInput{Query: "invoices"})
	mustContain(t, out.Content, "text search only", "e1 service → stores → invoices", "text #1")

	p.embedder = newWordEmbedder(map[string][]float32{
		"invoices": {1, 0, 0}, "billing": {0.9, 0.1, 0}, "sessions": {0, 1, 0}, "x": {0, 0, 1},
	})
	out, _ = palaceKGRecallHandler(ctx, p, KGRecallToolInput{Query: "billing"})
	mustContain(t, out.Content, "e1 service → stores → invoices", "vector #1")
	if strings.Contains(out.Content, "text search only") {
		t.Fatalf("fell back with an embedder: %s", out.Content)
	}
	out, _ = palaceKGRecallHandler(ctx, p, KGRecallToolInput{Query: "sessions", Graph: "billing"})
	if strings.Contains(out.Content, "cache") {
		t.Fatalf("graph scope ignored: %s", out.Content)
	}
}

func TestToolKG_BundleRoundTrip(t *testing.T) {
	src, dst := newToolPalace(t), newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	conf := 0.8
	palaceKGAddHandler(ctx, src, KGAddToolInput{Subject: "api", Predicate: "uses", Object: "postgres",
		Confidence: &conf, Source: "README", Graph: "infra"})
	exp, _ := palaceKGBundleHandler(ctx, src, KGBundleToolInput{Action: "export", EID: 1})
	if strings.HasPrefix(exp.Content, "Error") {
		t.Fatal(exp.Content)
	}
	imp, _ := palaceKGBundleHandler(ctx, dst, KGBundleToolInput{Action: "import", Bundle: exp.Content})
	mustContain(t, imp.Content, "Imported", "already known; root is e")
	again, _ := palaceKGBundleHandler(ctx, dst, KGBundleToolInput{Action: "import", Bundle: exp.Content})
	mustContain(t, again.Content, " 0 new")
	q, _ := palaceKGQueryHandler(ctx, dst, KGQueryToolInput{Entity: "api", Graph: "infra"})
	mustContain(t, q.Content, "postgres", "confidence 0.8", "source README")
	bad, _ := palaceKGBundleHandler(ctx, dst, KGBundleToolInput{Action: "import", Bundle: "{"})
	mustContain(t, bad.Content, "Error: bundle is not valid JSON")
}
