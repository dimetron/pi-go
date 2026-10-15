package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/cost"
	"github.com/dimetron/pi-go/internal/guardrail"
	pisession "github.com/dimetron/pi-go/internal/session"
)

func TestShowCost_NoSession(t *testing.T) {
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{WorkDir: t.TempDir()},
	}
	isolateCostEnv(t)
	m.showCostMessage()
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(m.chatModel.Messages))
	}
	got := m.chatModel.Messages[0].content
	if !strings.Contains(got, "Session cost") || !strings.Contains(got, "not available") {
		t.Errorf("cost message = %q", got)
	}
	if !strings.Contains(got, "stale") || !strings.Contains(got, "/model-price-refresh") || !strings.Contains(got, "modelPrices") {
		t.Errorf("cost message should say the estimate can be stale, got %q", got)
	}
}

func TestShowCost_PricesSessionLedger(t *testing.T) {
	isolateCostEnv(t)
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	resp, err := svc.Create(ctx, &session.CreateRequest{AppName: "pi-go", UserID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	id := resp.Session.ID()
	if err := svc.SetSessionModel(id, "gpt-5.6-sol"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSessionProvider(id, "openai", ""); err != nil {
		t.Fatal(err)
	}
	ev := &session.Event{ID: "e", Timestamp: time.Now(), Author: "model"}
	ev.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		// Under gpt-5.6-sol's 272k context tier, so the base rate applies:
		// 250k input at $4 / million is $1.00.
		PromptTokenCount: 250_000,
	}
	if err := svc.AppendEvent(ctx, resp.Session, ev); err != nil {
		t.Fatal(err)
	}

	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			WorkDir:        t.TempDir(),
			SessionService: svc,
			SessionID:      id,
			ProviderName:   "openai",
			ModelName:      "gpt-5.6-sol",
		},
	}
	m.handleSlashCommand("/cost")
	got := m.chatModel.Messages[len(m.chatModel.Messages)-1].content
	if !strings.Contains(got, "$1.00") {
		t.Errorf("cost message = %q, want $1.00", got)
	}
	if !strings.Contains(got, "openai / gpt-5.6-sol") {
		t.Errorf("cost message missing model line: %q", got)
	}
	if !strings.Contains(got, "**Total**: $1.00") {
		t.Errorf("cost message missing total: %q", got)
	}
	if strings.Contains(got, "context tier") {
		t.Errorf("a 250k prompt is under the 272k tier: %q", got)
	}
}

func TestShowCost_ConfigOverrideReplacesCatalog(t *testing.T) {
	isolateCostEnv(t)
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, ".pi-go"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"modelPrices":{"openai/gpt-5.6-sol":{"input":10,"output":0}}}`
	if err := os.WriteFile(filepath.Join(work, ".pi-go", "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	resp, err := svc.Create(ctx, &session.CreateRequest{AppName: "pi-go", UserID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	id := resp.Session.ID()
	if err := svc.SetSessionModel(id, "gpt-5.6-sol"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSessionProvider(id, "openai", ""); err != nil {
		t.Fatal(err)
	}
	ev := &session.Event{ID: "e", Timestamp: time.Now(), Author: "model"}
	ev.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 1_000_000}
	if err := svc.AppendEvent(ctx, resp.Session, ev); err != nil {
		t.Fatal(err)
	}
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			WorkDir:        work,
			SessionService: svc,
			SessionID:      id,
			ProviderName:   "openai",
			ModelName:      "gpt-5.6-sol",
		},
	}
	m.showCostMessage()
	got := m.chatModel.Messages[0].content
	if !strings.Contains(got, "$10.00") || !strings.Contains(got, "config rate") {
		t.Errorf("override not applied: %q", got)
	}
}

func TestShowCost_DailyUsesActiveModel(t *testing.T) {
	isolateCostEnv(t)
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			WorkDir:      t.TempDir(),
			ProviderName: "openai",
			ModelName:    "gpt-5.6-sol",
			TokenTracker: &dailyTracker{
				cplxTracker: &cplxTracker{},
				usage:       guardrail.Usage{InputTokens: 1_000_000, OutputTokens: 0, Requests: 2},
			},
		},
	}
	m.showCostMessage()
	got := m.chatModel.Messages[0].content
	if !strings.Contains(got, "*Today*") || !strings.Contains(got, "$4.00") {
		t.Errorf("daily section = %q", got)
	}
	if !strings.Contains(got, "not available") {
		t.Errorf("missing session note: %q", got)
	}
}

func TestFormatCostMessage_UnpricedOmittedFromTotal(t *testing.T) {
	groups := cost.ByModel([]cost.Result{{
		Provider: "local", Model: "none", Fresh: 10, Output: 1,
	}})
	got := formatCostMessage(groups, cost.Summarize([]cost.Result{{Provider: "local", Model: "none"}}), nil, "", "", "")
	if !strings.Contains(got, "**no price**") || !strings.Contains(got, "omitted") {
		t.Errorf("unpriced report = %q", got)
	}
	if !strings.Contains(got, "**Total**: $0.00") {
		t.Errorf("total = %q", got)
	}
}

// dailyTracker is a TokenTracker that also exposes the input/output split.
type dailyTracker struct {
	*cplxTracker
	usage guardrail.Usage
}

func (d *dailyTracker) Current() guardrail.Usage { return d.usage }

func isolateCostEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
}
