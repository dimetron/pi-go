package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

func TestSessionSpendAccumulatesAndIsStable(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	id := createSpendSession(t, svc)

	if err := svc.AppendEvent(ctx, mustGet(t, svc, id), usageEvent("e1", 1000, 100, 250, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := svc.AppendEvent(ctx, mustGet(t, svc, id), usageEvent("e2", 400, 40, 0, 10, 450)); err != nil {
		t.Fatal(err)
	}
	// A turn with no usage is not a spend row.
	plain := &session.Event{ID: "plain", Timestamp: time.Now(), Author: "user"}
	plain.Content = genai.NewContentFromText("hello", genai.RoleUser)
	if err := svc.AppendEvent(ctx, mustGet(t, svc, id), plain); err != nil {
		t.Fatal(err)
	}

	first, err := svc.SessionSpend(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 {
		t.Fatalf("spend rows = %d, want 2", len(first))
	}
	if first[0].Provider != "openai" || first[0].Model != "gpt-5.6-sol" {
		t.Errorf("first row model = %s/%s", first[0].Provider, first[0].Model)
	}
	if first[0].Input != 1000 || first[0].Output != 100 || first[0].Cached != 250 {
		t.Errorf("first row tokens = %+v", first[0])
	}
	if first[1].Thoughts != 10 || first[1].Total != 450 {
		t.Errorf("second row = %+v", first[1])
	}
	second, err := svc.SessionSpend(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != len(first) {
		t.Errorf("second read = %d rows, want %d", len(second), len(first))
	}
}

func TestSpendFollowsModelSwitchWithoutDroppingBaseURL(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	id := createSpendSession(t, svc)
	if err := svc.SetSessionProvider(id, "openai", "https://example.invalid/v1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AppendEvent(ctx, mustGet(t, svc, id), usageEvent("first", 10, 1, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSessionIdentity(id, "claude-sonnet-4-6", "anthropic"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AppendEvent(ctx, mustGet(t, svc, id), usageEvent("second", 20, 2, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	calls, err := svc.SessionSpend(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].Provider != "openai" || calls[0].Model != "gpt-5.6-sol" {
		t.Errorf("first call = %s/%s", calls[0].Provider, calls[0].Model)
	}
	if calls[1].Provider != "anthropic" || calls[1].Model != "claude-sonnet-4-6" {
		t.Errorf("second call = %s/%s", calls[1].Provider, calls[1].Model)
	}
	raw, err := os.ReadFile(filepath.Join(svc.baseDir, id, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "https://example.invalid/v1") {
		t.Errorf("base URL was dropped: %s", raw)
	}
}

func TestSessionSpendModelVersionWins(t *testing.T) {
	svc := newTestService(t)
	id := createSpendSession(t, svc)
	ev := usageEvent("e", 10, 1, 0, 0, 0)
	ev.ModelVersion = "gpt-5.6-sol-2026-07-09"
	if err := svc.AppendEvent(context.Background(), mustGet(t, svc, id), ev); err != nil {
		t.Fatal(err)
	}
	calls, err := svc.SessionSpend(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Model != "gpt-5.6-sol-2026-07-09" {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestSessionSpendPartialIsIgnored(t *testing.T) {
	svc := newTestService(t)
	id := createSpendSession(t, svc)
	ev := usageEvent("partial", 99, 9, 0, 0, 0)
	ev.Partial = true
	if err := svc.AppendEvent(context.Background(), mustGet(t, svc, id), ev); err != nil {
		t.Fatal(err)
	}
	calls, err := svc.SessionSpend(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("partial event recorded spend: %+v", calls)
	}
}

func TestSessionSpendSurvivesCompact(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	id := createSpendSession(t, svc)
	long := string(make([]byte, 400))
	for _, ev := range []*session.Event{
		usageEvent("old", 2000, 200, 100, 0, 0),
		usageEvent("new", 300, 30, 0, 0, 0),
	} {
		ev.Content = genai.NewContentFromText(long, genai.RoleModel)
		if err := svc.AppendEvent(ctx, mustGet(t, svc, id), ev); err != nil {
			t.Fatal(err)
		}
	}
	// Drop the ledger so compaction has to snapshot the events it is about
	// to discard, the way a session written before the ledger existed would.
	if err := os.Remove(filepath.Join(svc.baseDir, id, spendFileName)); err != nil {
		t.Fatal(err)
	}
	err := svc.Compact(id, "test-app", "test-user", func(events []*session.Event) (string, error) {
		return "summary", nil
	}, CompactConfig{MaxTokens: 1, KeepRecent: 1})
	if err != nil {
		t.Fatal(err)
	}
	calls, err := svc.SessionSpend(id)
	if err != nil {
		t.Fatal(err)
	}
	var in int64
	for _, c := range calls {
		in += c.Input
	}
	if len(calls) != 2 || in != 2300 {
		t.Fatalf("spend after compact = %+v", calls)
	}
}

func TestSessionSpendSurvivesClear(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	id := createSpendSession(t, svc)
	if err := svc.AppendEvent(ctx, mustGet(t, svc, id), usageEvent("e", 80, 8, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if err := svc.ClearEvents(id, "test-app", "test-user"); err != nil {
		t.Fatal(err)
	}
	calls, err := svc.SessionSpend(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Input != 80 {
		t.Fatalf("spend after clear = %+v", calls)
	}
}

func createSpendSession(t *testing.T, svc *FileService) string {
	t.Helper()
	resp, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
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
	return id
}

func mustGet(t *testing.T, svc *FileService, id string) session.Session {
	t.Helper()
	resp, err := svc.Get(context.Background(), &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: id,
	})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Session
}

func usageEvent(id string, in, out, cached, thoughts, total int32) *session.Event {
	ev := &session.Event{
		ID:        id,
		Timestamp: time.Now(),
		Author:    "model",
	}
	ev.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        in,
		CandidatesTokenCount:    out,
		CachedContentTokenCount: cached,
		ThoughtsTokenCount:      thoughts,
		TotalTokenCount:         total,
	}
	return ev
}
