package memory

import (
	"context"
	"strings"
	"testing"
	"time"
)

func setupContextTestDB(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return NewSQLiteStore(db)
}

func TestContextGenerator_EmptyProject(t *testing.T) {
	store := setupContextTestDB(t)
	gen := NewContextGenerator(store, 8000)

	result, err := gen.Generate(context.Background(), "/some/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty string for empty project, got %q", result)
	}
}

func TestContextGenerator_BasicOutput(t *testing.T) {
	store := setupContextTestDB(t)
	ctx := context.Background()

	// Create a session
	sess := &Session{
		SessionID: "sess-1",
		Project:   "/my/project",
		StartedAt: time.Now().Add(-1 * time.Hour),
		Status:    "active",
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Insert observations
	now := time.Now()
	obs := []*Observation{
		{
			SessionID: "sess-1", Project: "/my/project",
			Title: "Added retry logic", Type: TypeFeature,
			Text: "Implemented exponential backoff in API client",
			SourceFiles: []string{"internal/api/client.go"},
			ToolName: "Edit", DiscoveryTokens: 850,
			CreatedAt: now.Add(-30 * time.Minute),
		},
		{
			SessionID: "sess-1", Project: "/my/project",
			Title: "Fixed nil pointer", Type: TypeBugfix,
			Text: "Session store was returning nil on missing key",
			SourceFiles: []string{"internal/store/session.go"},
			ToolName: "Edit", DiscoveryTokens: 1200,
			CreatedAt: now.Add(-25 * time.Minute),
		},
	}
	for _, o := range obs {
		if err := store.InsertObservation(ctx, o); err != nil {
			t.Fatalf("insert observation: %v", err)
		}
	}

	gen := NewContextGenerator(store, 8000)
	result, err := gen.Generate(ctx, "/my/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify structure
	if !strings.Contains(result, "[project] recent context") {
		t.Error("missing header with project name")
	}
	if !strings.Contains(result, "Legend:") {
		t.Error("missing legend")
	}
	if !strings.Contains(result, "Session:") {
		t.Error("missing session header")
	}
	if !strings.Contains(result, "Added retry logic") {
		t.Error("missing first observation title")
	}
	if !strings.Contains(result, "Fixed nil pointer") {
		t.Error("missing second observation title")
	}
	if !strings.Contains(result, "feature") {
		t.Error("missing feature type")
	}
	if !strings.Contains(result, "bugfix") {
		t.Error("missing bugfix type")
	}
	if !strings.Contains(result, "mem-search") {
		t.Error("missing footer with tool references")
	}
	if !strings.Contains(result, "internal/api/client.go") {
		t.Error("missing source file grouping")
	}
}

func TestContextGenerator_TokenBudget(t *testing.T) {
	store := setupContextTestDB(t)
	ctx := context.Background()

	sess := &Session{
		SessionID: "sess-budget",
		Project:   "/budget/project",
		StartedAt: time.Now().Add(-1 * time.Hour),
		Status:    "active",
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Insert many observations to exceed budget
	now := time.Now()
	for i := 0; i < 50; i++ {
		obs := &Observation{
			SessionID: "sess-budget", Project: "/budget/project",
			Title: strings.Repeat("Long title text ", 10),
			Type:  TypeChange,
			Text:  strings.Repeat("This is a long observation text that should consume tokens. ", 20),
			SourceFiles: []string{"file.go"},
			ToolName: "Edit", DiscoveryTokens: 100,
			CreatedAt: now.Add(-time.Duration(50-i) * time.Minute),
		}
		if err := store.InsertObservation(ctx, obs); err != nil {
			t.Fatalf("insert observation %d: %v", i, err)
		}
	}

	// Use a very small budget
	gen := NewContextGenerator(store, 500)
	result, err := gen.Generate(ctx, "/budget/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have content but be truncated
	resultTokens := estimateTokens(result)
	// Allow some overhead for header/footer beyond budget
	if resultTokens > 800 {
		t.Errorf("result tokens %d exceeded budget + overhead, expected <= 800", resultTokens)
	}
}

func TestContextGenerator_CrossSessionGrouping(t *testing.T) {
	store := setupContextTestDB(t)
	ctx := context.Background()

	now := time.Now()

	// Create two sessions
	for _, sid := range []string{"sess-a", "sess-b"} {
		sess := &Session{
			SessionID: sid,
			Project:   "/cross/project",
			StartedAt: now.Add(-2 * time.Hour),
			Status:    "completed",
		}
		if err := store.CreateSession(ctx, sess); err != nil {
			t.Fatalf("create session: %v", err)
		}
	}

	// Observations in session A
	obsA := &Observation{
		SessionID: "sess-a", Project: "/cross/project",
		Title: "Session A work", Type: TypeFeature,
		Text: "Work from session A", SourceFiles: []string{"a.go"},
		ToolName: "Edit", CreatedAt: now.Add(-90 * time.Minute),
	}
	if err := store.InsertObservation(ctx, obsA); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Observations in session B
	obsB := &Observation{
		SessionID: "sess-b", Project: "/cross/project",
		Title: "Session B work", Type: TypeBugfix,
		Text: "Work from session B", SourceFiles: []string{"b.go"},
		ToolName: "Edit", CreatedAt: now.Add(-60 * time.Minute),
	}
	if err := store.InsertObservation(ctx, obsB); err != nil {
		t.Fatalf("insert: %v", err)
	}

	gen := NewContextGenerator(store, 8000)
	result, err := gen.Generate(ctx, "/cross/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both sessions should appear
	sessionCount := strings.Count(result, "## Session:")
	if sessionCount != 2 {
		t.Errorf("expected 2 session groups, got %d", sessionCount)
	}
	if !strings.Contains(result, "Session A work") {
		t.Error("missing session A observation")
	}
	if !strings.Contains(result, "Session B work") {
		t.Error("missing session B observation")
	}
}

func TestContextGenerator_WithSummary(t *testing.T) {
	store := setupContextTestDB(t)
	ctx := context.Background()

	now := time.Now()

	sess := &Session{
		SessionID: "sess-sum",
		Project:   "/sum/project",
		StartedAt: now.Add(-1 * time.Hour),
		Status:    "completed",
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Add a summary
	sum := &SessionSummary{
		SessionID: "sess-sum",
		Project:   "/sum/project",
		Request:   "Implement retry logic for API calls",
		Completed: "Added exponential backoff",
		CreatedAt: now.Add(-30 * time.Minute),
	}
	if err := store.UpsertSummary(ctx, sum); err != nil {
		t.Fatalf("upsert summary: %v", err)
	}

	// Add an observation
	obs := &Observation{
		SessionID: "sess-sum", Project: "/sum/project",
		Title: "Added backoff", Type: TypeFeature,
		Text: "Exponential backoff", SourceFiles: []string{"api.go"},
		ToolName: "Edit", CreatedAt: now.Add(-30 * time.Minute),
	}
	if err := store.InsertObservation(ctx, obs); err != nil {
		t.Fatalf("insert: %v", err)
	}

	gen := NewContextGenerator(store, 8000)
	result, err := gen.Generate(ctx, "/sum/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Session header should use summary request as title
	if !strings.Contains(result, "Implement retry logic for API calls") {
		t.Error("session header should use summary request as title")
	}
}

func TestContextGenerator_OldObservationsFiltered(t *testing.T) {
	store := setupContextTestDB(t)
	ctx := context.Background()

	sess := &Session{
		SessionID: "sess-old",
		Project:   "/old/project",
		StartedAt: time.Now().Add(-100 * time.Hour),
		Status:    "completed",
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Insert observation older than 72 hours
	obs := &Observation{
		SessionID: "sess-old", Project: "/old/project",
		Title: "Ancient work", Type: TypeChange,
		Text: "Very old observation", SourceFiles: []string{"old.go"},
		ToolName: "Edit", CreatedAt: time.Now().Add(-100 * time.Hour),
	}
	if err := store.InsertObservation(ctx, obs); err != nil {
		t.Fatalf("insert: %v", err)
	}

	gen := NewContextGenerator(store, 8000)
	result, err := gen.Generate(ctx, "/old/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be empty since all observations are too old
	if result != "" {
		t.Errorf("expected empty result for old observations, got %q", result)
	}
}

func TestContextGenerator_FileGrouping(t *testing.T) {
	store := setupContextTestDB(t)
	ctx := context.Background()

	now := time.Now()

	sess := &Session{
		SessionID: "sess-files",
		Project:   "/files/project",
		StartedAt: now.Add(-1 * time.Hour),
		Status:    "active",
	}
	if err := store.CreateSession(ctx, sess); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Multiple observations for same file
	for i, title := range []string{"First edit", "Second edit"} {
		obs := &Observation{
			SessionID: "sess-files", Project: "/files/project",
			Title: title, Type: TypeChange,
			Text: "Some text", SourceFiles: []string{"shared.go"},
			ToolName: "Edit", CreatedAt: now.Add(-time.Duration(30-i*5) * time.Minute),
		}
		if err := store.InsertObservation(ctx, obs); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Observation with no source files (grouped as "General")
	obs := &Observation{
		SessionID: "sess-files", Project: "/files/project",
		Title: "General work", Type: TypeDiscovery,
		Text: "No files", SourceFiles: []string{},
		ToolName: "Bash", CreatedAt: now.Add(-20 * time.Minute),
	}
	if err := store.InsertObservation(ctx, obs); err != nil {
		t.Fatalf("insert: %v", err)
	}

	gen := NewContextGenerator(store, 8000)
	result, err := gen.Generate(ctx, "/files/project")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(result, "**shared.go**") {
		t.Error("missing file group for shared.go")
	}
	if !strings.Contains(result, "**General**") {
		t.Error("missing General group for observations without source files")
	}
}

func TestContextGenerator_DefaultBudget(t *testing.T) {
	store := setupContextTestDB(t)
	gen := NewContextGenerator(store, 0)
	if gen.tokenBudget != 8000 {
		t.Errorf("expected default budget 8000, got %d", gen.tokenBudget)
	}
}

func TestFormatSessionTime(t *testing.T) {
	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{"zero", time.Time{}, "unknown time"},
		{"valid", time.Date(2026, 3, 20, 14, 30, 0, 0, time.UTC), "Mar 20 at 2:30 PM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSessionTime(tt.time)
			if got != tt.want {
				t.Errorf("formatSessionTime(%v) = %q, want %q", tt.time, got, tt.want)
			}
		})
	}
}

func TestTypeEmoji(t *testing.T) {
	tests := []struct {
		typ  ObservationType
		want string
	}{
		{TypeBugfix, "bugfix"},
		{TypeFeature, "feature"},
		{TypeRefactor, "refactor"},
		{TypeDiscovery, "discovery"},
		{TypeDecision, "decision"},
		{TypeChange, "change"},
		{ObservationType("unknown"), "unknown"},
	}
	for _, tt := range tests {
		got := typeEmoji(tt.typ)
		if got != tt.want {
			t.Errorf("typeEmoji(%q) = %q, want %q", tt.typ, got, tt.want)
		}
	}
}
