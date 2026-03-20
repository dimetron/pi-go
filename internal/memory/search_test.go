package memory

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSearch_FTS5_ByKeyword(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-search", "/project")

	base := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)

	// Insert 10 observations with distinct terms
	terms := []string{"quantum", "database", "quantum", "parser", "router", "quantum", "cache", "auth", "database", "flux"}
	for i, term := range terms {
		obs := &Observation{
			SessionID:       "sess-search",
			Project:         "/project",
			Title:           fmt.Sprintf("Implemented %s module", term),
			Type:            TypeFeature,
			Text:            fmt.Sprintf("Built the %s component with tests", term),
			SourceFiles:     []string{fmt.Sprintf("%s.go", term)},
			ToolName:        "Write",
			DiscoveryTokens: (i + 1) * 100,
			CreatedAt:       base.Add(time.Duration(i) * time.Minute),
		}
		if err := store.InsertObservation(ctx, obs); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// Search for "quantum" — should find 3 matches
	result, err := store.Search(ctx, SearchQuery{
		Query:   "quantum",
		Project: "/project",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 3 {
		t.Errorf("total = %d, want 3", result.Total)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(result.Rows))
	}

	// All results should mention quantum
	for _, row := range result.Rows {
		if row.Title == "" {
			t.Error("expected non-empty title")
		}
		if row.Type != TypeFeature {
			t.Errorf("type = %q, want feature", row.Type)
		}
		if row.ReadCost <= 0 {
			t.Error("expected positive ReadCost")
		}
	}
}

func TestSearch_ProjectScoping(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-a", "/project-a")
	insertTestSession(t, store, "sess-b", "/project-b")

	now := time.Now()

	// Insert into project-a
	store.InsertObservation(ctx, &Observation{
		SessionID: "sess-a", Project: "/project-a",
		Title: "alpha feature", Type: TypeFeature, Text: "alpha work",
		SourceFiles: []string{}, ToolName: "Write", CreatedAt: now,
	})
	// Insert into project-b
	store.InsertObservation(ctx, &Observation{
		SessionID: "sess-b", Project: "/project-b",
		Title: "alpha bugfix", Type: TypeBugfix, Text: "alpha fix",
		SourceFiles: []string{}, ToolName: "Edit", CreatedAt: now,
	})

	// Search for "alpha" scoped to project-a
	result, err := store.Search(ctx, SearchQuery{
		Query:   "alpha",
		Project: "/project-a",
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("total = %d, want 1 (project-b excluded)", result.Total)
	}

	// Search without project scoping
	result, err = store.Search(ctx, SearchQuery{Query: "alpha"})
	if err != nil {
		t.Fatalf("Search all: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("total = %d, want 2 (both projects)", result.Total)
	}
}

func TestSearch_TypeFilter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-type", "/project")
	now := time.Now()

	store.InsertObservation(ctx, &Observation{
		SessionID: "sess-type", Project: "/project",
		Title: "found widget bug", Type: TypeBugfix, Text: "widget was broken",
		SourceFiles: []string{}, ToolName: "Read", CreatedAt: now,
	})
	store.InsertObservation(ctx, &Observation{
		SessionID: "sess-type", Project: "/project",
		Title: "added widget feature", Type: TypeFeature, Text: "widget now flies",
		SourceFiles: []string{}, ToolName: "Write", CreatedAt: now.Add(time.Second),
	})

	result, err := store.Search(ctx, SearchQuery{
		Query: "widget",
		Type:  TypeBugfix,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("total = %d, want 1 (only bugfix)", result.Total)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	store := newTestStore(t)
	result, err := store.Search(context.Background(), SearchQuery{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("total = %d, want 0 for empty query", result.Total)
	}
}

func TestSearch_NoResults(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-empty", "/project")
	store.InsertObservation(ctx, &Observation{
		SessionID: "sess-empty", Project: "/project",
		Title: "hello world", Type: TypeChange, Text: "greeting",
		SourceFiles: []string{}, ToolName: "Write", CreatedAt: time.Now(),
	})

	result, err := store.Search(ctx, SearchQuery{Query: "zzzznonexistent"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("total = %d, want 0", result.Total)
	}
	if len(result.Rows) != 0 {
		t.Errorf("rows = %d, want 0", len(result.Rows))
	}
}

func TestSearch_Pagination(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-page", "/project")
	now := time.Now()

	// Insert 10 observations all matching "pagination"
	for i := 0; i < 10; i++ {
		store.InsertObservation(ctx, &Observation{
			SessionID: "sess-page", Project: "/project",
			Title: fmt.Sprintf("pagination test %d", i), Type: TypeChange,
			Text: "pagination content", SourceFiles: []string{}, ToolName: "Read",
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}

	// Page 1: limit 3, offset 0
	result, err := store.Search(ctx, SearchQuery{Query: "pagination", Limit: 3, Offset: 0})
	if err != nil {
		t.Fatalf("Search page 1: %v", err)
	}
	if result.Total != 10 {
		t.Errorf("total = %d, want 10", result.Total)
	}
	if len(result.Rows) != 3 {
		t.Errorf("rows = %d, want 3", len(result.Rows))
	}

	// Page 4: limit 3, offset 9 — should get 1 result
	result, err = store.Search(ctx, SearchQuery{Query: "pagination", Limit: 3, Offset: 9})
	if err != nil {
		t.Fatalf("Search page 4: %v", err)
	}
	if result.Total != 10 {
		t.Errorf("total = %d, want 10", result.Total)
	}
	if len(result.Rows) != 1 {
		t.Errorf("rows = %d, want 1", len(result.Rows))
	}
}

func TestTimeline(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-timeline", "/project")
	base := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)

	// Insert 10 sequential observations
	var ids []int64
	for i := 0; i < 10; i++ {
		obs := &Observation{
			SessionID:   "sess-timeline",
			Project:     "/project",
			Title:       fmt.Sprintf("step-%d", i),
			Type:        TypeChange,
			Text:        fmt.Sprintf("did step %d", i),
			SourceFiles: []string{},
			ToolName:    "Edit",
			CreatedAt:   base.Add(time.Duration(i) * time.Minute),
		}
		if err := store.InsertObservation(ctx, obs); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
		ids = append(ids, obs.ID)
	}

	// Timeline around #5 (step-4), before=2, after=2
	timeline, err := store.Timeline(ctx, ids[4], 2, 2)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}

	// Should have: step-2, step-3 (before) + step-4 (anchor) + step-5, step-6 (after) = 5
	if len(timeline) != 5 {
		t.Fatalf("timeline len = %d, want 5", len(timeline))
	}

	// Verify chronological order
	expected := []string{"step-2", "step-3", "step-4", "step-5", "step-6"}
	for i, want := range expected {
		if timeline[i].Title != want {
			t.Errorf("timeline[%d].Title = %q, want %q", i, timeline[i].Title, want)
		}
	}
}

func TestTimeline_AtEdges(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-edge", "/project")
	base := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)

	var ids []int64
	for i := 0; i < 5; i++ {
		obs := &Observation{
			SessionID: "sess-edge", Project: "/project",
			Title: fmt.Sprintf("edge-%d", i), Type: TypeChange,
			Text: "text", SourceFiles: []string{}, ToolName: "Read",
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		store.InsertObservation(ctx, obs)
		ids = append(ids, obs.ID)
	}

	// Timeline around first observation — no "before"
	timeline, err := store.Timeline(ctx, ids[0], 3, 3)
	if err != nil {
		t.Fatalf("Timeline at start: %v", err)
	}
	// Should be: edge-0 (anchor) + edge-1, edge-2, edge-3 (after) = 4
	if len(timeline) != 4 {
		t.Errorf("timeline at start len = %d, want 4", len(timeline))
	}
	if timeline[0].Title != "edge-0" {
		t.Errorf("first = %q, want edge-0", timeline[0].Title)
	}

	// Timeline around last observation — no "after"
	timeline, err = store.Timeline(ctx, ids[4], 3, 3)
	if err != nil {
		t.Fatalf("Timeline at end: %v", err)
	}
	// Should be: edge-1, edge-2, edge-3 (before) + edge-4 (anchor) = 4
	if len(timeline) != 4 {
		t.Errorf("timeline at end len = %d, want 4", len(timeline))
	}
	if timeline[len(timeline)-1].Title != "edge-4" {
		t.Errorf("last = %q, want edge-4", timeline[len(timeline)-1].Title)
	}
}

func TestTimeline_AnchorNotFound(t *testing.T) {
	store := newTestStore(t)
	_, err := store.Timeline(context.Background(), 99999, 3, 3)
	if err == nil {
		t.Error("expected error for nonexistent anchor")
	}
}

func TestTimeline_ProjectIsolation(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-iso-a", "/project-a")
	insertTestSession(t, store, "sess-iso-b", "/project-b")
	base := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)

	// Interleave observations from two projects
	var anchorID int64
	for i := 0; i < 6; i++ {
		proj := "/project-a"
		sid := "sess-iso-a"
		if i%2 == 1 {
			proj = "/project-b"
			sid = "sess-iso-b"
		}
		obs := &Observation{
			SessionID: sid, Project: proj,
			Title: fmt.Sprintf("iso-%d", i), Type: TypeChange,
			Text: "text", SourceFiles: []string{}, ToolName: "Read",
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		store.InsertObservation(ctx, obs)
		if i == 2 { // anchor is iso-2 in project-a
			anchorID = obs.ID
		}
	}

	timeline, err := store.Timeline(ctx, anchorID, 5, 5)
	if err != nil {
		t.Fatalf("Timeline: %v", err)
	}

	// Only project-a observations should appear: iso-0, iso-2 (anchor), iso-4
	for _, obs := range timeline {
		if obs.Project != "/project-a" {
			t.Errorf("got observation from %q, want /project-a only", obs.Project)
		}
	}
}

func TestSearchSummaries(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	base := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		sid := fmt.Sprintf("sess-ssum-%d", i)
		insertTestSession(t, store, sid, "/project")
		store.UpsertSummary(ctx, &SessionSummary{
			SessionID:    sid,
			Project:      "/project",
			Request:      fmt.Sprintf("request %d", i),
			Investigated: "investigated stuff",
			Learned:      fmt.Sprintf("learned about %s", []string{"caching", "routing", "caching", "auth", "caching"}[i]),
			Completed:    "done",
			CreatedAt:    base.Add(time.Duration(i) * time.Hour),
		})
	}

	// Search for "caching" in summaries
	results, err := store.SearchSummaries(ctx, "caching", "/project")
	if err != nil {
		t.Fatalf("SearchSummaries: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("got %d summaries, want 3", len(results))
	}
}

func TestSearchSummaries_Empty(t *testing.T) {
	store := newTestStore(t)
	results, err := store.SearchSummaries(context.Background(), "", "/project")
	if err != nil {
		t.Fatalf("SearchSummaries: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for empty query, got %v", results)
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"abcd", 1},
		{"hello world!", 3},
	}
	for _, tt := range tests {
		got := estimateTokens(tt.input)
		if got != tt.want {
			t.Errorf("estimateTokens(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestSearch_DefaultLimitAndOffset(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-defaults", "/project")
	now := time.Now()
	for i := 0; i < 5; i++ {
		store.InsertObservation(ctx, &Observation{
			SessionID: "sess-defaults", Project: "/project",
			Title: fmt.Sprintf("item %d", i), Type: TypeChange,
			Text: "default limit test content", SourceFiles: []string{}, ToolName: "Read",
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}

	// Limit=0 should default to 20, Offset=-1 should default to 0.
	result, err := store.Search(ctx, SearchQuery{Query: "default", Limit: 0, Offset: -1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if result.Total != 5 {
		t.Errorf("total = %d, want 5", result.Total)
	}
	if len(result.Rows) != 5 {
		t.Errorf("rows = %d, want 5", len(result.Rows))
	}
}

func TestTimeline_DefaultBeforeAfter(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-defba", "/project")
	base := time.Date(2026, 3, 20, 10, 0, 0, 0, time.UTC)

	var anchorID int64
	for i := 0; i < 11; i++ {
		obs := &Observation{
			SessionID: "sess-defba", Project: "/project",
			Title: fmt.Sprintf("t-%d", i), Type: TypeChange,
			Text: "text", SourceFiles: []string{}, ToolName: "Read",
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		store.InsertObservation(ctx, obs)
		if i == 5 {
			anchorID = obs.ID
		}
	}

	// before=0 and after=0 both default to 5 each.
	timeline, err := store.Timeline(ctx, anchorID, 0, 0)
	if err != nil {
		t.Fatalf("Timeline(0,0): %v", err)
	}
	// anchor (1) + up to 5 before + up to 5 after = 11 total.
	if len(timeline) != 11 {
		t.Errorf("timeline len = %d, want 11", len(timeline))
	}
}

func TestSearchSummaries_NoProject(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		sid := fmt.Sprintf("sess-np-%d", i)
		proj := fmt.Sprintf("/proj-%d", i)
		insertTestSession(t, store, sid, proj)
		store.UpsertSummary(ctx, &SessionSummary{
			SessionID: sid, Project: proj,
			Request: fmt.Sprintf("searched keyword item %d", i),
			Learned: "details", CreatedAt: time.Now(),
		})
	}

	// Search without project filter — should find all 3.
	results, err := store.SearchSummaries(ctx, "keyword", "")
	if err != nil {
		t.Fatalf("SearchSummaries: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("got %d results, want 3", len(results))
	}
}

func TestSearchLike_DirectCall(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-like", "/project")
	now := time.Now()

	store.InsertObservation(ctx, &Observation{
		SessionID: "sess-like", Project: "/project",
		Title: "awesome implementation", Type: TypeFeature,
		Text: "used the awesome framework", SourceFiles: []string{}, ToolName: "Write",
		CreatedAt: now,
	})
	store.InsertObservation(ctx, &Observation{
		SessionID: "sess-like", Project: "/project",
		Title: "boring bugfix", Type: TypeBugfix,
		Text: "nothing special here", SourceFiles: []string{}, ToolName: "Edit",
		CreatedAt: now.Add(time.Second),
	})

	// Call searchLike directly (bypasses HasFTS5 check). Limit must be > 0.
	result, err := store.searchLike(ctx, SearchQuery{Query: "awesome", Limit: 20})
	if err != nil {
		t.Fatalf("searchLike: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("total = %d, want 1", result.Total)
	}
	if len(result.Rows) != 1 {
		t.Errorf("rows = %d, want 1", len(result.Rows))
	}
}

func TestSearchLike_WithProjectAndTypeFilters(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	insertTestSession(t, store, "sess-likefilter-a", "/proj-a")
	insertTestSession(t, store, "sess-likefilter-b", "/proj-b")
	now := time.Now()

	store.InsertObservation(ctx, &Observation{
		SessionID: "sess-likefilter-a", Project: "/proj-a",
		Title: "zeta feature", Type: TypeFeature,
		Text: "zeta work done", SourceFiles: []string{}, ToolName: "Write",
		CreatedAt: now,
	})
	store.InsertObservation(ctx, &Observation{
		SessionID: "sess-likefilter-b", Project: "/proj-b",
		Title: "zeta bugfix", Type: TypeBugfix,
		Text: "zeta bug fixed", SourceFiles: []string{}, ToolName: "Edit",
		CreatedAt: now.Add(time.Second),
	})

	// Scoped to /proj-a only.
	result, err := store.searchLike(ctx, SearchQuery{Query: "zeta", Project: "/proj-a"})
	if err != nil {
		t.Fatalf("searchLike with project: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("total = %d, want 1", result.Total)
	}

	// Scoped by type bugfix.
	result, err = store.searchLike(ctx, SearchQuery{Query: "zeta", Type: TypeBugfix})
	if err != nil {
		t.Fatalf("searchLike with type: %v", err)
	}
	if result.Total != 1 {
		t.Errorf("total bugfix = %d, want 1", result.Total)
	}

	// Pagination.
	result, err = store.searchLike(ctx, SearchQuery{Query: "zeta", Limit: 1, Offset: 0})
	if err != nil {
		t.Fatalf("searchLike with limit: %v", err)
	}
	if result.Total != 2 {
		t.Errorf("total all = %d, want 2", result.Total)
	}
	if len(result.Rows) != 1 {
		t.Errorf("rows = %d, want 1 (limited)", len(result.Rows))
	}
}

func TestSearchSummariesLike_DirectCall(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		sid := fmt.Sprintf("sess-sumlike-%d", i)
		proj := "/proj"
		insertTestSession(t, store, sid, proj)
		store.UpsertSummary(ctx, &SessionSummary{
			SessionID: sid, Project: proj,
			Request:   fmt.Sprintf("unique-term task %d", i),
			Learned:   "some learning",
			CreatedAt: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	// Without project filter.
	results, err := store.searchSummariesLike(ctx, "unique-term", "")
	if err != nil {
		t.Fatalf("searchSummariesLike: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("got %d results, want 3", len(results))
	}

	// With project filter.
	results, err = store.searchSummariesLike(ctx, "unique-term", "/proj")
	if err != nil {
		t.Fatalf("searchSummariesLike with project: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("got %d results with project, want 3", len(results))
	}

	// No match.
	results, err = store.searchSummariesLike(ctx, "xyz-not-found", "")
	if err != nil {
		t.Fatalf("searchSummariesLike no match: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("got %d results, want 0", len(results))
	}
}

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", `"hello"`},
		{"hello world", `"hello" "world"`},
		{`he"llo`, `"hello"`},
		{"", ""},
	}
	for _, tt := range tests {
		got := sanitizeFTS5Query(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
