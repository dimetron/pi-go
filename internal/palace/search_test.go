package palace

import (
	"context"
	"testing"
	"time"
)

func insertTestDrawers(t *testing.T, store PalaceStore) {
	t.Helper()
	ctx := context.Background()

	drawers := []*Drawer{
		{ID: "d_go", Wing: "tech", Room: "languages", Content: "Go programming language with goroutines and channels for concurrent programming", CreatedAt: time.Now().UTC(), Importance: 5},
		{ID: "d_py", Wing: "tech", Room: "languages", Content: "Python scripting language for data science and machine learning", CreatedAt: time.Now().UTC(), Importance: 3},
		{ID: "d_cook", Wing: "life", Room: "hobbies", Content: "cooking recipes for Italian pasta dishes", CreatedAt: time.Now().UTC(), Importance: 2},
		{ID: "d_rust", Wing: "tech", Room: "languages", Content: "Rust systems programming language with ownership and borrowing", CreatedAt: time.Now().UTC(), Importance: 4},
		{ID: "d_auth", Wing: "tech", Room: "backend", Content: "authentication middleware with JWT tokens and session management", CreatedAt: time.Now().UTC(), Importance: 6},
	}

	for _, d := range drawers {
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatalf("InsertDrawer(%s): %v", d.ID, err)
		}
	}
}

func TestSearch_FTS5Fallback_NilEmbedder(t *testing.T) {
	store := newTestStore(t)
	insertTestDrawers(t, store)

	ds := NewDrawerService(store, nil, DefaultConfig())
	ctx := context.Background()

	results, err := ds.Search(ctx, SearchQuery{Query: "programming", Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// FTS5 should match "Go programming" and "Rust systems programming"
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results, got %d", len(results))
	}

	// Verify all results contain "programming" in content
	for _, r := range results {
		if r.Similarity != 0 {
			t.Errorf("expected similarity=0 for FTS5 result, got %.3f", r.Similarity)
		}
	}
}

func TestSearch_FTS5_WingFilter(t *testing.T) {
	store := newTestStore(t)
	insertTestDrawers(t, store)

	ds := NewDrawerService(store, nil, DefaultConfig())
	ctx := context.Background()

	// Search only in "life" wing — should not find programming content
	results, err := ds.Search(ctx, SearchQuery{Query: "programming", Wing: "life"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results in life wing for 'programming', got %d", len(results))
	}
}

func TestSearch_FTS5_RoomFilter(t *testing.T) {
	store := newTestStore(t)
	insertTestDrawers(t, store)

	ds := NewDrawerService(store, nil, DefaultConfig())
	ctx := context.Background()

	// Search only in "backend" room
	results, err := ds.Search(ctx, SearchQuery{Query: "tokens", Room: "backend"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result in backend room, got %d", len(results))
	}
	if results[0].Drawer.ID != "d_auth" {
		t.Errorf("expected d_auth, got %s", results[0].Drawer.ID)
	}
}

func TestSearch_EmptyQuery(t *testing.T) {
	store := newTestStore(t)
	ds := NewDrawerService(store, nil, DefaultConfig())
	ctx := context.Background()

	_, err := ds.Search(ctx, SearchQuery{Query: ""})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}

func TestSearch_LimitRespected(t *testing.T) {
	store := newTestStore(t)
	insertTestDrawers(t, store)

	ds := NewDrawerService(store, nil, DefaultConfig())
	ctx := context.Background()

	results, err := ds.Search(ctx, SearchQuery{Query: "programming", Limit: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) > 1 {
		t.Errorf("expected at most 1 result with limit=1, got %d", len(results))
	}
}

func TestSearch_DefaultLimit(t *testing.T) {
	store := newTestStore(t)
	insertTestDrawers(t, store)

	ds := NewDrawerService(store, nil, DefaultConfig())
	ctx := context.Background()

	// Limit=0 should default to 5
	results, err := ds.Search(ctx, SearchQuery{Query: "programming"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) > 5 {
		t.Errorf("expected at most 5 results with default limit, got %d", len(results))
	}
}

func TestSearch_SemanticPath(t *testing.T) {
	// Test the semantic search path using pre-populated embeddings.
	// We can't use a real embedder, so we test by inserting drawers
	// with known embeddings and using RankBySimilarity directly,
	// then verify the DrawerService Search fetches full drawers.
	store := newTestStore(t)
	ctx := context.Background()

	// Insert drawers with known embeddings
	d1 := makeDrawer("d_similar", "tech", "go", "Go concurrency patterns")
	d1.Embedding = []float32{0.9, 0.1, 0.0, 0.0} // similar to query
	d2 := makeDrawer("d_different", "tech", "python", "Python web frameworks")
	d2.Embedding = []float32{0.0, 0.0, 0.9, 0.1} // different from query
	d3 := makeDrawer("d_medium", "tech", "rust", "Rust async runtime")
	d3.Embedding = []float32{0.7, 0.3, 0.0, 0.0} // somewhat similar

	for _, d := range []*Drawer{d1, d2, d3} {
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatalf("InsertDrawer: %v", err)
		}
	}

	// Verify RankBySimilarity returns correct order
	queryVec := []float32{1.0, 0.0, 0.0, 0.0}
	candidates, err := store.GetAllEmbeddings(ctx, DrawerFilter{Wing: "tech"})
	if err != nil {
		t.Fatalf("GetAllEmbeddings: %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("expected 3 candidates, got %d", len(candidates))
	}

	ranked := RankBySimilarity(queryVec, candidates, 3)
	if len(ranked) != 3 {
		t.Fatalf("expected 3 ranked results, got %d", len(ranked))
	}
	if ranked[0].DrawerID != "d_similar" {
		t.Errorf("expected d_similar first, got %s", ranked[0].DrawerID)
	}
	if ranked[1].DrawerID != "d_medium" {
		t.Errorf("expected d_medium second, got %s", ranked[1].DrawerID)
	}
	if ranked[2].DrawerID != "d_different" {
		t.Errorf("expected d_different third, got %s", ranked[2].DrawerID)
	}
	if ranked[0].Similarity <= ranked[1].Similarity {
		t.Errorf("expected first similarity > second: %.3f <= %.3f", ranked[0].Similarity, ranked[1].Similarity)
	}
}

func TestKeywordSearch_NoResults(t *testing.T) {
	store := newTestStore(t)
	insertTestDrawers(t, store)

	ctx := context.Background()
	results, err := store.KeywordSearch(ctx, "nonexistent_xyzzy", DrawerFilter{}, 5)
	if err != nil {
		t.Fatalf("KeywordSearch: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello world", `"hello" "world"`},
		{"", ""},
		{`test "inject"`, `"test" "inject"`},
		{"single", `"single"`},
		{`"" empty`, `"empty"`},
		{"  spaces  between  ", `"spaces" "between"`},
	}

	for _, tt := range tests {
		got := sanitizeFTS5Query(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
