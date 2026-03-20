package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/memory"
)

// memMockStore implements memory.Store for tool tests.
type memMockStore struct {
	observations []*memory.Observation
	searchResult *memory.SearchResult
	searchErr    error
	timelineErr  error
	getErr       error
}

func (s *memMockStore) Search(_ context.Context, q memory.SearchQuery) (*memory.SearchResult, error) {
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	if s.searchResult != nil {
		return s.searchResult, nil
	}
	return &memory.SearchResult{Rows: []memory.SearchResultRow{}}, nil
}

func (s *memMockStore) Timeline(_ context.Context, anchorID int64, before, after int) ([]*memory.Observation, error) {
	if s.timelineErr != nil {
		return nil, s.timelineErr
	}
	return s.observations, nil
}

func (s *memMockStore) GetObservations(_ context.Context, ids []int64) ([]*memory.Observation, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	// Return observations matching requested IDs
	var result []*memory.Observation
	for _, id := range ids {
		for _, obs := range s.observations {
			if obs.ID == id {
				result = append(result, obs)
			}
		}
	}
	return result, nil
}

func (s *memMockStore) CreateSession(context.Context, *memory.Session) error         { return nil }
func (s *memMockStore) CompleteSession(context.Context, string) error                { return nil }
func (s *memMockStore) InsertObservation(context.Context, *memory.Observation) error { return nil }
func (s *memMockStore) RecentObservations(context.Context, string, int) ([]*memory.Observation, error) {
	return nil, nil
}
func (s *memMockStore) UpsertSummary(context.Context, *memory.SessionSummary) error { return nil }
func (s *memMockStore) RecentSummaries(context.Context, string, int) ([]*memory.SessionSummary, error) {
	return nil, nil
}
func (s *memMockStore) Close() error { return nil }

func TestMemSearchHandler(t *testing.T) {
	now := time.Now()

	t.Run("empty query returns error message", func(t *testing.T) {
		store := &memMockStore{}
		out, err := memSearchHandler(context.Background(), store, MemSearchInput{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Content, "Error") {
			t.Error("expected error message for empty query")
		}
	})

	t.Run("no results", func(t *testing.T) {
		store := &memMockStore{}
		out, err := memSearchHandler(context.Background(), store, MemSearchInput{Query: "nonexistent"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Content, "No observations") {
			t.Errorf("expected no-results message, got: %s", out.Content)
		}
		if out.Total != 0 {
			t.Errorf("expected total=0, got %d", out.Total)
		}
	})

	t.Run("returns formatted results", func(t *testing.T) {
		store := &memMockStore{
			searchResult: &memory.SearchResult{
				Total: 2,
				Rows: []memory.SearchResultRow{
					{ID: 1, Title: "Added auth module", Type: memory.TypeFeature, CreatedAt: now, ReadCost: 100},
					{ID: 2, Title: "Fixed login bug", Type: memory.TypeBugfix, CreatedAt: now, ReadCost: 50},
				},
			},
		}
		out, err := memSearchHandler(context.Background(), store, MemSearchInput{Query: "auth"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Total != 2 {
			t.Errorf("expected total=2, got %d", out.Total)
		}
		if !strings.Contains(out.Content, "Added auth module") {
			t.Error("expected title in output")
		}
		if !strings.Contains(out.Content, "#1") {
			t.Error("expected ID in output")
		}
		if !strings.Contains(out.Content, "mem-get") {
			t.Error("expected mem-get hint in output")
		}
	})

	t.Run("store error propagated", func(t *testing.T) {
		store := &memMockStore{searchErr: context.DeadlineExceeded}
		_, err := memSearchHandler(context.Background(), store, MemSearchInput{Query: "test"})
		if err == nil {
			t.Error("expected error from store")
		}
	})
}

func TestMemTimelineHandler(t *testing.T) {
	now := time.Now()
	obs := []*memory.Observation{
		{ID: 4, Title: "Before", Type: memory.TypeChange, CreatedAt: now.Add(-time.Minute), SourceFiles: []string{"a.go"}},
		{ID: 5, Title: "Anchor", Type: memory.TypeFeature, CreatedAt: now},
		{ID: 6, Title: "After", Type: memory.TypeChange, CreatedAt: now.Add(time.Minute)},
	}

	t.Run("zero anchor returns error message", func(t *testing.T) {
		store := &memMockStore{}
		out, err := memTimelineHandler(context.Background(), store, MemTimelineInput{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Content, "Error") {
			t.Error("expected error message for zero anchor")
		}
	})

	t.Run("returns formatted timeline", func(t *testing.T) {
		store := &memMockStore{observations: obs}
		out, err := memTimelineHandler(context.Background(), store, MemTimelineInput{Anchor: 5})
		if err != nil {
			t.Fatal(err)
		}
		if out.Count != 3 {
			t.Errorf("expected count=3, got %d", out.Count)
		}
		if !strings.Contains(out.Content, "Anchor") {
			t.Error("expected anchor title")
		}
		if !strings.Contains(out.Content, ">>>") {
			t.Error("expected anchor marker")
		}
		if !strings.Contains(out.Content, "a.go") {
			t.Error("expected source files in output")
		}
	})

	t.Run("store error propagated", func(t *testing.T) {
		store := &memMockStore{timelineErr: context.DeadlineExceeded}
		_, err := memTimelineHandler(context.Background(), store, MemTimelineInput{Anchor: 1})
		if err == nil {
			t.Error("expected error from store")
		}
	})
}

func TestMemGetHandler(t *testing.T) {
	now := time.Now()
	obs := []*memory.Observation{
		{
			ID: 10, Title: "Auth refactor", Type: memory.TypeRefactor,
			Text: "Refactored authentication to use JWT", ToolName: "edit",
			SourceFiles: []string{"auth.go", "jwt.go"}, CreatedAt: now,
		},
	}

	t.Run("empty IDs returns error message", func(t *testing.T) {
		store := &memMockStore{}
		out, err := memGetHandler(context.Background(), store, MemGetInput{})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Content, "Error") {
			t.Error("expected error message for empty IDs")
		}
	})

	t.Run("returns formatted observations", func(t *testing.T) {
		store := &memMockStore{observations: obs}
		out, err := memGetHandler(context.Background(), store, MemGetInput{IDs: []int64{10}})
		if err != nil {
			t.Fatal(err)
		}
		if out.Count != 1 {
			t.Errorf("expected count=1, got %d", out.Count)
		}
		if !strings.Contains(out.Content, "Auth refactor") {
			t.Error("expected title")
		}
		if !strings.Contains(out.Content, "JWT") {
			t.Error("expected text content")
		}
		if !strings.Contains(out.Content, "auth.go") {
			t.Error("expected source files")
		}
		if !strings.Contains(out.Content, "edit") {
			t.Error("expected tool name")
		}
	})

	t.Run("no matching IDs", func(t *testing.T) {
		store := &memMockStore{observations: obs}
		out, err := memGetHandler(context.Background(), store, MemGetInput{IDs: []int64{999}})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Content, "No observations") {
			t.Errorf("expected no-results message, got: %s", out.Content)
		}
	})

	t.Run("store error propagated", func(t *testing.T) {
		store := &memMockStore{getErr: context.DeadlineExceeded}
		_, err := memGetHandler(context.Background(), store, MemGetInput{IDs: []int64{1}})
		if err == nil {
			t.Error("expected error from store")
		}
	})
}

func TestMemoryTools(t *testing.T) {
	t.Run("nil store returns nil", func(t *testing.T) {
		tools, err := MemoryTools(nil)
		if err != nil {
			t.Fatal(err)
		}
		if tools != nil {
			t.Error("expected nil tools for nil store")
		}
	})

	t.Run("returns 3 tools", func(t *testing.T) {
		store := &memMockStore{}
		tools, err := MemoryTools(store)
		if err != nil {
			t.Fatal(err)
		}
		if len(tools) != 3 {
			t.Errorf("expected 3 memory tools, got %d", len(tools))
		}

		expected := map[string]bool{
			"mem-search":   true,
			"mem-timeline": true,
			"mem-get":      true,
		}
		for _, tool := range tools {
			if !expected[tool.Name()] {
				t.Errorf("unexpected tool: %s", tool.Name())
			}
			delete(expected, tool.Name())
		}
		for name := range expected {
			t.Errorf("missing tool: %s", name)
		}
	})
}

func TestFormatSearchResults(t *testing.T) {
	now := time.Now()
	result := &memory.SearchResult{
		Total: 1,
		Rows: []memory.SearchResultRow{
			{ID: 42, Title: "Test obs", Type: memory.TypeDiscovery, CreatedAt: now, ReadCost: 200},
		},
	}
	content := formatSearchResults(result)
	if !strings.Contains(content, "#42") {
		t.Error("expected ID")
	}
	if !strings.Contains(content, "discovery") {
		t.Error("expected type")
	}
	if !strings.Contains(content, "200t") {
		t.Error("expected token cost")
	}
}

func TestFormatTimeline(t *testing.T) {
	now := time.Now()
	obs := []*memory.Observation{
		{ID: 1, Title: "First", Type: memory.TypeChange, CreatedAt: now},
		{ID: 2, Title: "Second", Type: memory.TypeFeature, CreatedAt: now, SourceFiles: []string{"x.go"}},
	}
	content := formatTimeline(obs, 2)
	if !strings.Contains(content, ">>>") {
		t.Error("expected anchor marker on ID 2")
	}
	if !strings.Contains(content, "x.go") {
		t.Error("expected source files")
	}
}

func TestFormatObservations(t *testing.T) {
	now := time.Now()
	obs := []*memory.Observation{
		{ID: 1, Title: "Obs 1", Type: memory.TypeBugfix, Text: "Fixed bug", CreatedAt: now, ToolName: "bash"},
		{ID: 2, Title: "Obs 2", Type: memory.TypeFeature, Text: "Added feature", CreatedAt: now, SourceFiles: []string{"f.go"}},
	}
	content := formatObservations(obs)
	if !strings.Contains(content, "---") {
		t.Error("expected separator between observations")
	}
	if !strings.Contains(content, "Fixed bug") {
		t.Error("expected observation text")
	}
	if !strings.Contains(content, "f.go") {
		t.Error("expected source files")
	}
}
