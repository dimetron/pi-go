package palace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLayers_L0_ReadsIdentityFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	idFile := filepath.Join(dir, "identity.txt")
	if err := os.WriteFile(idFile, []byte("I am Pi, a coding assistant."), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.IdentityFile = idFile

	ms := NewMemoryStack(nil, nil, cfg)
	got, err := ms.loadIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if got != "I am Pi, a coding assistant." {
		t.Fatalf("expected identity text, got %q", got)
	}
}

func TestLayers_L0_MissingFileReturnsEmpty(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.IdentityFile = "/nonexistent/identity.txt"

	ms := NewMemoryStack(nil, nil, cfg)
	got, err := ms.loadIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestLayers_L0_NoIdentityFileConfigured(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	ms := NewMemoryStack(nil, nil, cfg)
	got, err := ms.loadIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestLayers_L1_TopDrawersByImportance(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	drawers := []*Drawer{
		{ID: "d1", Wing: "proj", Room: "auth", Content: "Auth logic for login flow", Importance: 8, CreatedAt: now},
		{ID: "d2", Wing: "proj", Room: "auth", Content: "Token validation helpers", Importance: 5, CreatedAt: now},
		{ID: "d3", Wing: "proj", Room: "db", Content: "Database migration scripts", Importance: 10, CreatedAt: now},
		{ID: "d4", Wing: "proj", Room: "api", Content: "REST endpoint definitions", Importance: 3, CreatedAt: now},
		{ID: "d5", Wing: "other", Room: "misc", Content: "Unrelated wing content", Importance: 9, CreatedAt: now},
	}
	for _, d := range drawers {
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.L1TopK = 3

	ms := NewMemoryStack(store, nil, cfg)
	got, err := ms.loadEssentialStory(ctx, "proj")
	if err != nil {
		t.Fatal(err)
	}

	// Should include the top 3 by importance from wing "proj": d3(10), d1(8), d2(5).
	if !strings.Contains(got, "Database migration scripts") {
		t.Error("expected top-importance drawer d3")
	}
	if !strings.Contains(got, "Auth logic for login flow") {
		t.Error("expected high-importance drawer d1")
	}
	if !strings.Contains(got, "Token validation helpers") {
		t.Error("expected medium-importance drawer d2")
	}
	// d4 (importance=3) should be excluded since topK=3.
	if strings.Contains(got, "REST endpoint") {
		t.Error("low-importance drawer should be excluded with topK=3")
	}
	// d5 is from a different wing.
	if strings.Contains(got, "Unrelated wing") {
		t.Error("drawer from different wing should be excluded")
	}
}

func TestLayers_L1_RespectsMaxChars(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 0; i < 20; i++ {
		d := &Drawer{
			ID:         layersTestID("max", i),
			Wing:       "proj",
			Room:       "room",
			Content:    strings.Repeat("x", 200),
			Importance: 5,
			CreatedAt:  now,
		}
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.L1TopK = 20
	cfg.L1MaxChars = 500

	ms := NewMemoryStack(store, nil, cfg)
	got, err := ms.loadEssentialStory(ctx, "proj")
	if err != nil {
		t.Fatal(err)
	}

	if len(got) > 500 {
		t.Errorf("L1 output exceeds max chars: %d > 500", len(got))
	}
}

func TestLayers_L1_EmptyPalace(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	ms := NewMemoryStack(store, nil, DefaultConfig())
	got, err := ms.loadEssentialStory(ctx, "proj")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected empty for empty palace, got %q", got)
	}
}

func TestLayers_L1_AllWingsWhenEmpty(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	d := &Drawer{ID: "d1", Wing: "proj", Room: "auth", Content: "Auth stuff", Importance: 5, CreatedAt: now}
	if err := store.InsertDrawer(ctx, d); err != nil {
		t.Fatal(err)
	}

	ms := NewMemoryStack(store, nil, DefaultConfig())
	got, err := ms.loadEssentialStory(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "Auth stuff") {
		t.Error("expected drawer from any wing when wing filter is empty")
	}
}

func TestLayers_L2_FiltersByWingAndRoom(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	drawers := []*Drawer{
		{ID: "d1", Wing: "proj", Room: "auth", Content: "Auth detail 1", CreatedAt: now},
		{ID: "d2", Wing: "proj", Room: "auth", Content: "Auth detail 2", CreatedAt: now},
		{ID: "d3", Wing: "proj", Room: "db", Content: "DB detail", CreatedAt: now},
		{ID: "d4", Wing: "other", Room: "auth", Content: "Other wing auth", CreatedAt: now},
	}
	for _, d := range drawers {
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	ms := NewMemoryStack(store, nil, DefaultConfig())
	got, err := ms.Recall(ctx, "proj", "auth")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "Auth detail 1") || !strings.Contains(got, "Auth detail 2") {
		t.Error("expected both auth drawers from proj wing")
	}
	if strings.Contains(got, "DB detail") {
		t.Error("should not include drawers from different room")
	}
	if strings.Contains(got, "Other wing auth") {
		t.Error("should not include drawers from different wing")
	}
}

func TestLayers_L2_RespectsMaxDrawers(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 0; i < 20; i++ {
		d := &Drawer{
			ID:        layersTestID("l2", i),
			Wing:      "proj",
			Room:      "room",
			Content:   strings.Repeat("y", 50),
			CreatedAt: now,
		}
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.L2MaxDrawers = 3

	ms := NewMemoryStack(store, nil, cfg)
	got, err := ms.Recall(ctx, "proj", "room")
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(got, "\n")
	bulletCount := 0
	for _, l := range lines {
		if strings.HasPrefix(l, "- ") {
			bulletCount++
		}
	}
	if bulletCount != 3 {
		t.Errorf("expected 3 bullets, got %d", bulletCount)
	}
}

func TestLayers_L2_EmptyResult(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	ms := NewMemoryStack(store, nil, DefaultConfig())
	got, err := ms.Recall(ctx, "nonexistent", "room")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected empty for nonexistent wing/room, got %q", got)
	}
}

func TestLayers_WakeUp_CombinesL0L1(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	dir := t.TempDir()
	idFile := filepath.Join(dir, "identity.txt")
	if err := os.WriteFile(idFile, []byte("I am Pi, a coding assistant."), 0644); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	drawers := []*Drawer{
		{ID: "d1", Wing: "proj", Room: "auth", Content: "Auth is JWT-based", Importance: 8, CreatedAt: now},
		{ID: "d2", Wing: "proj", Room: "db", Content: "Uses PostgreSQL 16", Importance: 7, CreatedAt: now},
	}
	for _, d := range drawers {
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.IdentityFile = idFile

	ms := NewMemoryStack(store, nil, cfg)
	got, err := ms.WakeUp(ctx, "proj")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "## Identity") {
		t.Error("expected Identity section")
	}
	if !strings.Contains(got, "I am Pi, a coding assistant.") {
		t.Error("expected identity content")
	}
	if !strings.Contains(got, "## Essential Knowledge") {
		t.Error("expected Essential Knowledge section")
	}
	if !strings.Contains(got, "Auth is JWT-based") {
		t.Error("expected L1 drawer content")
	}
}

func TestLayers_WakeUp_EmptyPalaceAndNoIdentity(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	ms := NewMemoryStack(store, nil, DefaultConfig())
	got, err := ms.WakeUp(ctx, "proj")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Fatalf("expected empty wake-up, got %q", got)
	}
}

func TestLayers_WakeUp_IdentityOnlyNoDrawers(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	dir := t.TempDir()
	idFile := filepath.Join(dir, "identity.txt")
	if err := os.WriteFile(idFile, []byte("I am Pi."), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	cfg.IdentityFile = idFile

	ms := NewMemoryStack(store, nil, cfg)
	got, err := ms.WakeUp(ctx, "proj")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "I am Pi.") {
		t.Error("expected identity content")
	}
	if strings.Contains(got, "Essential Knowledge") {
		t.Error("should not have Essential Knowledge section with no drawers")
	}
}

func TestLayers_TokenBudget(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC()
	for i := 0; i < 20; i++ {
		d := &Drawer{
			ID:         layersTestID("tok", i),
			Wing:       "proj",
			Room:       "room",
			Content:    strings.Repeat("word ", 100), // ~500 chars
			Importance: 20 - i,
			CreatedAt:  now,
		}
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.L1TopK = 15
	cfg.L1MaxChars = 3200

	ms := NewMemoryStack(store, nil, cfg)
	got, err := ms.loadEssentialStory(ctx, "proj")
	if err != nil {
		t.Fatal(err)
	}

	approxTokens := len(got) / 4
	if approxTokens > 800 {
		t.Errorf("approximate token count %d exceeds budget (3200 chars / 4 = 800 tokens)", approxTokens)
	}
	if len(got) > cfg.L1MaxChars {
		t.Errorf("L1 output %d chars exceeds L1MaxChars %d", len(got), cfg.L1MaxChars)
	}
}

func TestLayers_TruncateChars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		max      int
		expected string
	}{
		{"hello world", 20, "hello world"},
		{"hello world", 5, "hello…"},
		{"", 10, ""},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc…"},
	}

	for _, tt := range tests {
		got := truncateChars(tt.input, tt.max)
		if got != tt.expected {
			t.Errorf("truncateChars(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.expected)
		}
	}
}

// layersTestID creates a deterministic test ID for layers tests.
func layersTestID(prefix string, index int) string {
	return GenerateDrawerID(prefix, "test", "", index, strings.Repeat("x", index+1))
}
