package palace

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

// namedCtx is a tool context whose agent and session are known, so
// transactions carry an author and a source.
type namedCtx struct{ mockToolCtx }

func (namedCtx) AgentName() string { return "scribe" }
func (namedCtx) SessionID() string { return "s1" }

func toolCtx() namedCtx { return namedCtx{mockToolCtx{Context: context.Background()}} }

func TestToolKGCov_Graph(t *testing.T) {
	p := newToolPalace(t)
	ctx := toolCtx()
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "api", Predicate: "uses", Object: "postgres"})
	call := func(in KGGraphToolInput) string {
		t.Helper()
		out, err := palaceKGGraphHandler(ctx, p, in)
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		return out.Content
	}
	mustContain(t, call(KGGraphToolInput{Action: "list"}), "No named graphs yet.")
	mustContain(t, call(KGGraphToolInput{}), "No named graphs yet.")
	mustContain(t, call(KGGraphToolInput{Action: "list", EID: 1}), "e1 is in no named graph.")
	mustContain(t, call(KGGraphToolInput{Action: "add", EID: 1, Graph: "Infra Stack"}), "e1 added to graph infra_stack.")
	mustContain(t, call(KGGraphToolInput{Action: "add", EID: 1, Graph: "Infra Stack"}), "e1 is already in graph infra_stack.")
	mustContain(t, call(KGGraphToolInput{Action: "list", EID: 1}), "e1 is in: infra_stack")
	mustContain(t, call(KGGraphToolInput{Action: "list"}), "Named graphs:", "infra_stack (1 facts)")
	mustContain(t, call(KGGraphToolInput{Action: "remove", EID: 1, Graph: "infra stack"}), "e1 removed from graph infra_stack.")
	mustContain(t, call(KGGraphToolInput{Action: "remove", EID: 1, Graph: "infra stack"}), "e1 was not in graph infra_stack.")
	mustContain(t, call(KGGraphToolInput{Action: "add", EID: 0, Graph: "g"}), "Error: eid and graph are required")
	mustContain(t, call(KGGraphToolInput{Action: "remove", EID: 1, Graph: "  "}), "Error: eid and graph are required")
	mustContain(t, call(KGGraphToolInput{Action: "add", EID: 999, Graph: "g"}), "Error:")
	mustContain(t, call(KGGraphToolInput{Action: "drop"}), "Error: action must be add, remove or list")
	// The membership carries the agent and session as its author and source.
	tr, err := p.KG().Transaction(ctx, 2)
	if err != nil || tr == nil || tr.Author != "scribe" || tr.Source != "session:s1" {
		t.Errorf("transaction metadata = %+v, %v", tr, err)
	}
}

type failingEmbedder struct{ err error }

func (f failingEmbedder) Embed([]string) ([][]float32, error) { return nil, f.err }
func (failingEmbedder) Close()                                {}

func TestToolKGCov_Recall(t *testing.T) {
	p := newToolPalace(t)
	ctx := toolCtx()
	mustContain(t, func() string { o, _ := palaceKGRecallHandler(ctx, p, KGRecallToolInput{Query: "  "}); return o.Content }(),
		"Error: query is required")
	// Without an embedder the text channel answers; nothing matches in an empty graph.
	out, _ := palaceKGRecallHandler(ctx, p, KGRecallToolInput{Query: "anything"})
	mustContain(t, out.Content, "text search only", "Nothing recalled.")
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "service", Predicate: "stores", Object: "invoices"})
	out, _ = palaceKGRecallHandler(ctx, p, KGRecallToolInput{Query: "invoices", Limit: -3})
	mustContain(t, out.Content, "e1 service → stores → invoices", "text #1")
	// A failing embedder is an error, not a silent fallback to text.
	p.embedder = failingEmbedder{err: errors.New("model offline")}
	out, _ = palaceKGRecallHandler(ctx, p, KGRecallToolInput{Query: "invoices"})
	mustContain(t, out.Content, "Error:", "model offline")
	if strings.Contains(out.Content, "text search only") {
		t.Errorf("fell back to text on a real embedder failure: %s", out.Content)
	}
	// An embedding model is named by backend, so vectors never mix.
	p.embedder = nil
	p.config.ModelPath = "/models/minilm"
	if got := p.kgEmbedModel(); got != "local/minilm" {
		t.Errorf("local model name = %q", got)
	}
	p.embedder = &ollamaEmbedder{}
	p.config.OllamaModel = "nomic"
	if got := p.kgEmbedModel(); got != "ollama/nomic" {
		t.Errorf("ollama model name = %q", got)
	}
}

func TestToolKGCov_Bundle(t *testing.T) {
	p := newToolPalace(t)
	ctx := toolCtx()
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "api", Predicate: "uses", Object: "postgres"})
	call := func(in KGBundleToolInput) string {
		t.Helper()
		out, err := palaceKGBundleHandler(ctx, p, in)
		if err != nil {
			t.Fatalf("handler error: %v", err)
		}
		return out.Content
	}
	mustContain(t, call(KGBundleToolInput{Action: "export"}), "Error: eid is required")
	mustContain(t, call(KGBundleToolInput{Action: "export", EID: 1, TxAsOf: "garbage"}), "Error:")
	mustContain(t, call(KGBundleToolInput{Action: "export", EID: 999}), "Error:")
	mustContain(t, call(KGBundleToolInput{Action: "export", EID: 1, TxAsOf: "tx0"}), "Error:") // before anything was believed
	mustContain(t, call(KGBundleToolInput{Action: "sync"}), "Error: action must be export or import")
	exp := call(KGBundleToolInput{Action: "export", EID: 1, TxAsOf: "tx2"})
	if strings.HasPrefix(exp, "Error") {
		t.Fatal(exp)
	}
	dst := newToolPalace(t)
	out, _ := palaceKGBundleHandler(ctx, dst, KGBundleToolInput{Action: "import", Bundle: exp, Source: "colleague"})
	mustContain(t, out.Content, "Imported", "root is e1")
	tr, err := dst.KG().Transaction(ctx, 1)
	if err != nil || tr == nil || tr.Source != "colleague" {
		t.Errorf("import source = %+v, %v", tr, err)
	}
	bad, _ := palaceKGBundleHandler(ctx, dst, KGBundleToolInput{Action: "import", Bundle: `{"version":99}`})
	mustContain(t, bad.Content, "Error:", "unsupported bundle version")
}

func eidOf(t *testing.T, p *Palace, s, pred, o string) int64 {
	t.Helper()
	sv, pv, ov := Ent(s), Ent(pred), Ent(o)
	ms, err := p.KG().History().Match(context.Background(), Pattern{Subject: &sv, Predicate: &pv, Object: &ov})
	if err != nil || len(ms) == 0 {
		t.Fatalf("no statement %s %s %s: %v", s, pred, o, err)
	}
	return ms[len(ms)-1].EID
}

func TestToolKGCov_ReviseExplainConflicts(t *testing.T) {
	p := newToolPalace(t)
	ctx := toolCtx()
	conf := 0.9
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "api", Predicate: "uses", Object: "postgres", Confidence: &conf, Source: "README"})
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "api", Predicate: "uses", Object: "mysql", Source: "notes"})
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "api", Predicate: "uses", Object: "postgres", Source: "wiki", ConfirmExisting: true})
	pg, my := eidOf(t, p, "api", "uses", "postgres"), eidOf(t, p, "api", "uses", "mysql")
	palaceKGAddHandler(ctx, p, KGAddToolInput{Subject: "api", Predicate: "needs", Object: "dba", Support: []int64{pg}})
	dba := eidOf(t, p, "api", "needs", "dba")
	rev := func(in KGReviseToolInput) string {
		t.Helper()
		out, err := palaceKGReviseHandler(ctx, p, in)
		if err != nil {
			t.Fatal(err)
		}
		return out.Content
	}
	mustContain(t, rev(KGReviseToolInput{Action: "retract"}), "Error: eid is required")
	mustContain(t, rev(KGReviseToolInput{Action: "bogus", EID: pg}), "Error: action must be")
	mustContain(t, rev(KGReviseToolInput{Action: "supersede", EID: pg}), "Error: supersede needs")
	mustContain(t, rev(KGReviseToolInput{Action: "supersede", EID: pg, ValidFrom: "nope"}), "invalid valid_from")
	mustContain(t, rev(KGReviseToolInput{Action: "supersede", EID: pg, ValidTo: "nope"}), "invalid valid_to")
	mustContain(t, rev(KGReviseToolInput{Action: "retract", EID: 9999}), "Error:")
	mustContain(t, rev(KGReviseToolInput{Action: "supersede", EID: my, Object: "free text", Literal: true, DryRun: true, Reason: "typo"}),
		"Dry run, nothing written.", "Superseded e"+eidStr(my))
	mustContain(t, rev(KGReviseToolInput{Action: "confirm", EID: my, Source: "audit"}), "Confirmed e"+eidStr(my))

	ex, _ := palaceKGExplainHandler(ctx, p, KGExplainToolInput{EID: dba})
	mustContain(t, ex.Content, "Derived from:", "e"+eidStr(pg)+": api → uses → postgres", "by scribe", "from session:s1")
	ex, _ = palaceKGExplainHandler(ctx, p, KGExplainToolInput{EID: pg})
	mustContain(t, ex.Content, "Confidence: 0.9", "Sources: README", "Confirmed: tx", "Retracting it would also withdraw:")
	for in, want := range map[KGExplainToolInput]string{
		{}:                        "Error: eid is required",
		{EID: pg, TxAsOf: "junk"}: "Error:",
		{EID: 9999}:               "",
	} {
		out, _ := palaceKGExplainHandler(ctx, p, in)
		if want != "" {
			mustContain(t, out.Content, want)
		} else if out.Content == "" {
			t.Error("explain of a missing statement returned nothing")
		}
	}

	// Conflicts: two databases for one subject, with their evidence.
	cf, _ := palaceKGConflictsHandler(ctx, p, KGConflictsToolInput{Subject: "api", Predicate: "uses", Limit: 1})
	mustContain(t, cf.Content, "Conflicts (1)", "by scribe", "confidence 0.9", "sources README", "confirmed 1×")
	cf, _ = palaceKGConflictsHandler(ctx, p, KGConflictsToolInput{Subject: "nobody"})
	mustContain(t, cf.Content, "No conflicting facts.")
	cf, _ = palaceKGConflictsHandler(ctx, p, KGConflictsToolInput{TxAsOf: "junk"})
	mustContain(t, cf.Content, "Error:")

	// Supersede and retract show up in the explanations.
	out := rev(KGReviseToolInput{Action: "supersede", EID: my, Object: "sqlite"})
	mustContain(t, out, "Superseded e"+eidStr(my)+" → e", "annotation(s) carried over")
	ex, _ = palaceKGExplainHandler(ctx, p, KGExplainToolInput{EID: my})
	mustContain(t, ex.Content, "Corrected by:", "superseded")
	sq := eidOf(t, p, "api", "uses", "sqlite")
	ex, _ = palaceKGExplainHandler(ctx, p, KGExplainToolInput{EID: sq})
	mustContain(t, ex.Content, "Corrects: e"+eidStr(my))
	mustContain(t, rev(KGReviseToolInput{Action: "retract", EID: pg}), "Retracted e"+eidStr(pg), "Also withdrawn:", "e"+eidStr(dba))
	mustContain(t, rev(KGReviseToolInput{Action: "retract", EID: pg}), "already retracted")
}

func eidStr(n int64) string { return strconv.FormatInt(n, 10) }
