package palace

import (
	"context"
	"strings"
	"testing"
)

func newToolPalace(t *testing.T) *Palace {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	p := NewWithStore(NewSQLitePalaceStore(db), nil)
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}

func TestToolKG_EvidenceReviseExplain(t *testing.T) {
	p := newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	conf := 0.7

	out, _ := palaceKGAddHandler(ctx, p, KGAddToolInput{
		Subject: "project", Predicate: "database", Object: "postgres", Confidence: &conf, Source: "README.md",
	})
	mustContain(t, out.Content, "Triple added", "(id=e1)", "confidence 0.7", "source README.md")

	out, _ = palaceKGAddHandler(ctx, p, KGAddToolInput{
		Subject: "project", Predicate: "database", Object: "postgres", ConfirmExisting: true,
	})
	mustContain(t, out.Content, "Already known, confirmed", "(id=e1)", "confirmed 1×")

	// A conclusion derived from e1.
	out, _ = palaceKGAddHandler(ctx, p, KGAddToolInput{
		Subject: "tests", Predicate: "need", Object: "docker", Support: []int64{1},
	})
	mustContain(t, out.Content, "Triple added")
	derivedID := strings.TrimSuffix(strings.SplitN(strings.SplitN(out.Content, "(id=", 2)[1], ")", 2)[0], "")

	ex, _ := palaceKGExplainHandler(ctx, p, KGExplainToolInput{EID: 1})
	mustContain(t, ex.Content, "## e1: project → database → postgres", "Confidence: 0.7", "Sources: README.md",
		"Confirmed: tx", "Retracting it would also withdraw", derivedID)

	// Preview, then correct.
	rv, _ := palaceKGReviseHandler(ctx, p, KGReviseToolInput{Action: "supersede", EID: 1, Object: "sqlite", DryRun: true})
	mustContain(t, rv.Content, "Dry run, nothing written", "Superseded e1")
	if q, _ := palaceKGQueryHandler(ctx, p, KGQueryToolInput{Entity: "project"}); !strings.Contains(q.Content, "postgres") {
		t.Fatalf("dry run wrote: %s", q.Content)
	}
	rv, _ = palaceKGReviseHandler(ctx, p, KGReviseToolInput{
		Action: "supersede", EID: 1, Object: "sqlite", Reason: "go.mod imports modernc sqlite", Source: "go.mod",
	})
	mustContain(t, rv.Content, "Superseded e1 → e", "annotation(s) carried over", "Also withdrawn:", derivedID)

	q, _ := palaceKGQueryHandler(ctx, p, KGQueryToolInput{Entity: "project"})
	mustContain(t, q.Content, "sqlite", "confidence 0.7", "source README.md")
	if strings.Contains(q.Content, "postgres") {
		t.Errorf("corrected value still current: %s", q.Content)
	}
	// What was believed before the correction.
	q, _ = palaceKGQueryHandler(ctx, p, KGQueryToolInput{Entity: "project", TxAsOf: "3"})
	mustContain(t, q.Content, "postgres")

	tl, _ := palaceKGTimelineHandler(ctx, p, KGTimelineToolInput{Entity: "project"})
	mustContain(t, tl.Content, "superseded by e", "sqlite", "postgres")

	// Text search, retract, errors.
	q, _ = palaceKGQueryHandler(ctx, p, KGQueryToolInput{Text: "sqlite"})
	mustContain(t, q.Content, `text "sqlite"`, "project")

	rv, _ = palaceKGReviseHandler(ctx, p, KGReviseToolInput{Action: "retract", EID: 1})
	mustContain(t, rv.Content, "Error", "already retracted")
	for _, in := range []KGReviseToolInput{
		{Action: "supersede", EID: 99, Object: "x"},
		{Action: "supersede", EID: 2},
		{Action: "frobnicate", EID: 2},
		{Action: "confirm"},
		{Action: "supersede", EID: 2, ValidFrom: "soon"},
	} {
		if rv, _ := palaceKGReviseHandler(ctx, p, in); !strings.HasPrefix(rv.Content, "Error") {
			t.Errorf("revise %+v = %s, want an error", in, rv.Content)
		}
	}
	if ex, _ := palaceKGExplainHandler(ctx, p, KGExplainToolInput{EID: 999}); !strings.Contains(ex.Content, "No statement") {
		t.Errorf("explain missing = %s", ex.Content)
	}
	if ex, _ := palaceKGExplainHandler(ctx, p, KGExplainToolInput{EID: 1}); !strings.Contains(ex.Content, "superseded") ||
		!strings.Contains(ex.Content, "Corrected by") {
		t.Errorf("explain of the old version = %s", ex.Content)
	}
}

func TestToolKG_Conflicts(t *testing.T) {
	p := newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	if out, _ := palaceKGConflictsHandler(ctx, p, KGConflictsToolInput{}); out.Content != "No conflicting facts." {
		t.Errorf("empty = %s", out.Content)
	}
	hi := 0.9
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "ci", Predicate: "runs_on", Object: "github_actions"})
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "ci", Predicate: "runs_on", Object: "buildkite", Confidence: &hi, Source: "Makefile"})
	out, _ := palaceKGConflictsHandler(ctx, p, KGConflictsToolInput{Subject: "ci"})
	mustContain(t, out.Content, "### ci → runs_on", "github_actions: e1", "buildkite: e2", "confidence 0.9", "sources Makefile")
	if out, _ := palaceKGConflictsHandler(ctx, p, KGConflictsToolInput{TxAsOf: "not-a-time"}); !strings.HasPrefix(out.Content, "Error") {
		t.Errorf("bad tx_as_of = %s", out.Content)
	}
}

func TestToolKG_AddAboutLiteralAndValidation(t *testing.T) {
	p := newToolPalace(t)
	ctx := mockToolCtx{Context: context.Background()}
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "auth", Predicate: "uses", Object: "jwt"})
	out, _ := palaceKGAddHandler(ctx, p, KGAddToolInput{About: 1, Predicate: "note", Object: "tokens expire after 1h", Literal: true})
	mustContain(t, out.Content, "Triple added: e1 → note → tokens expire after 1h")
	for _, in := range []KGAddToolInput{
		{Predicate: "p", Object: "o"},
		{Subject: "s", Predicate: "p", Object: "o", ValidTo: "later"},
		{Subject: "s", Predicate: "p", Object: "o", ValidFrom: "2026-02-01", ValidTo: "2026-01-01"},
		{Subject: "s", Predicate: "p", Object: "o", Confidence: new(1.5)},
		{About: 42, Predicate: "p", Object: "o"},
	} {
		if out, _ := palaceKGAddHandler(ctx, p, in); !strings.HasPrefix(out.Content, "Error") {
			t.Errorf("add %+v = %s, want an error", in, out.Content)
		}
	}
	if out, _ := palaceKGQueryHandler(ctx, p, KGQueryToolInput{}); !strings.HasPrefix(out.Content, "Error") {
		t.Errorf("query without entity or text = %s", out.Content)
	}
}
