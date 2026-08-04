package palace

import (
	"context"
	"errors"
	"testing"
)

// newTestDrawerService creates an in-memory store + DrawerService with a fakeEmbedder.
// The fakeEmbedder uses a stub Embedder wrapper so we can inject mock vectors.
func newTestDrawerService(t *testing.T, embedder Embedder) *DrawerService {
	t.Helper()
	store := newTestStore(t)
	cfg := DefaultConfig()
	cfg.DeduplicationThreshold = 0.9
	return NewDrawerService(store, embedder, cfg)
}

func TestAddDrawer_NilEmbedder(t *testing.T) {
	ds := newTestDrawerService(t, nil)
	ctx := context.Background()

	drawer, err := ds.AddDrawer(ctx, DrawerInput{
		Wing:    "backend",
		Room:    "auth",
		Content: "JWT token validation logic",
	})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}
	if drawer.ID == "" {
		t.Error("expected non-empty drawer ID")
	}
	if len(drawer.Embedding) != 0 {
		t.Errorf("expected no embedding with nil embedder, got %d floats", len(drawer.Embedding))
	}

	// Verify it was persisted
	got, err := ds.store.GetDrawer(ctx, drawer.ID)
	if err != nil {
		t.Fatalf("GetDrawer: %v", err)
	}
	if got.Content != "JWT token validation logic" {
		t.Errorf("content = %q, want JWT token validation logic", got.Content)
	}
}

func TestAddDrawer_EmptyContent(t *testing.T) {
	ds := newTestDrawerService(t, nil)
	ctx := context.Background()

	_, err := ds.AddDrawer(ctx, DrawerInput{Wing: "w", Room: "r"})
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestAddDrawer_EmptyWingOrRoom(t *testing.T) {
	ds := newTestDrawerService(t, nil)
	ctx := context.Background()

	_, err := ds.AddDrawer(ctx, DrawerInput{Wing: "", Room: "r", Content: "x"})
	if err == nil {
		t.Fatal("expected error for empty wing")
	}

	_, err = ds.AddDrawer(ctx, DrawerInput{Wing: "w", Room: "", Content: "x"})
	if err == nil {
		t.Fatal("expected error for empty room")
	}
}

func TestAddDrawer_WithEmbeddingAndDedup(t *testing.T) {
	// We can't easily use a real Embedder in unit tests (needs model download),
	// so we test the DrawerService logic with a store that has pre-populated
	// embeddings and verify the dedup flow via CheckDuplicate.
	// For the AddDrawer path with embedder, we test the nil-embedder path and
	// the DuplicateError type separately.

	store := newTestStore(t)
	ctx := context.Background()

	// Insert a drawer with a known embedding directly via store.
	vec := []float32{1, 0, 0, 0}
	d := makeDrawer("existing", "backend", "auth", "original content")
	d.Embedding = vec
	if err := store.InsertDrawer(ctx, d); err != nil {
		t.Fatalf("InsertDrawer: %v", err)
	}

	// Verify embedding stored
	emb, err := store.GetEmbedding(ctx, "existing")
	if err != nil {
		t.Fatalf("GetEmbedding: %v", err)
	}
	if len(emb.Embedding) != 4 {
		t.Fatalf("expected 4 floats, got %d", len(emb.Embedding))
	}

	// Check that FindDuplicates works on this embedding
	candidates, err := store.GetAllEmbeddings(ctx, DrawerFilter{Wing: "backend", Room: "auth"})
	if err != nil {
		t.Fatalf("GetAllEmbeddings: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	// Same vector should be a duplicate
	dupes := FindDuplicates(vec, candidates, 0.9)
	if len(dupes) != 1 {
		t.Fatalf("expected 1 duplicate, got %d", len(dupes))
	}
	if dupes[0].ExistingID != "existing" {
		t.Errorf("duplicate ID = %q, want existing", dupes[0].ExistingID)
	}
	if dupes[0].Similarity < 0.99 {
		t.Errorf("similarity = %.3f, want >= 0.99", dupes[0].Similarity)
	}

	// Different vector should not be a duplicate
	diffVec := []float32{0, 1, 0, 0}
	dupes = FindDuplicates(diffVec, candidates, 0.9)
	if len(dupes) != 0 {
		t.Errorf("expected no duplicates for orthogonal vector, got %d", len(dupes))
	}
}

func TestDeleteDrawer_ViaService(t *testing.T) {
	ds := newTestDrawerService(t, nil)
	ctx := context.Background()

	drawer, err := ds.AddDrawer(ctx, DrawerInput{
		Wing:    "backend",
		Room:    "auth",
		Content: "to delete",
	})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}

	if err := ds.DeleteDrawer(ctx, drawer.ID); err != nil {
		t.Fatalf("DeleteDrawer: %v", err)
	}

	// Verify gone
	_, err = ds.store.GetDrawer(ctx, drawer.ID)
	if err == nil {
		t.Error("expected error after delete")
	}
}

func TestDeleteDrawer_NotFound(t *testing.T) {
	ds := newTestDrawerService(t, nil)
	ctx := context.Background()

	err := ds.DeleteDrawer(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent drawer")
	}
}

func TestCheckDuplicate_NilEmbedder(t *testing.T) {
	ds := newTestDrawerService(t, nil)
	ctx := context.Background()

	result, err := ds.CheckDuplicate(ctx, "some content", DrawerFilter{})
	if err != nil {
		t.Fatalf("CheckDuplicate: %v", err)
	}
	if result != nil {
		t.Error("expected nil result with nil embedder")
	}
}

func TestDuplicateError(t *testing.T) {
	err := &DuplicateError{Result: DuplicateResult{
		ExistingID: "d123",
		Similarity: 0.95,
	}}

	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}

	var de *DuplicateError
	if !errors.As(err, &de) {
		t.Error("expected DuplicateError to be unwrappable")
	}
	if de.Result.ExistingID != "d123" {
		t.Errorf("ExistingID = %q, want d123", de.Result.ExistingID)
	}
}

func TestAddDrawer_SetsFieldsCorrectly(t *testing.T) {
	ds := newTestDrawerService(t, nil)
	ctx := context.Background()

	drawer, err := ds.AddDrawer(ctx, DrawerInput{
		Wing:       "frontend",
		Room:       "ui",
		Hall:       "components",
		Content:    "Button component styles",
		SourceFile: "src/Button.tsx",
		ChunkIndex: 2,
		AddedBy:    "miner",
		Importance: 7,
	})
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}

	if drawer.Wing != "frontend" {
		t.Errorf("wing = %q, want frontend", drawer.Wing)
	}
	if drawer.Room != "ui" {
		t.Errorf("room = %q, want ui", drawer.Room)
	}
	if drawer.Hall != "components" {
		t.Errorf("hall = %q, want components", drawer.Hall)
	}
	if drawer.SourceFile != "src/Button.tsx" {
		t.Errorf("source_file = %q, want src/Button.tsx", drawer.SourceFile)
	}
	if drawer.ChunkIndex != 2 {
		t.Errorf("chunk_index = %d, want 2", drawer.ChunkIndex)
	}
	if drawer.AddedBy != "miner" {
		t.Errorf("added_by = %q, want miner", drawer.AddedBy)
	}
	if drawer.Importance != 7 {
		t.Errorf("importance = %d, want 7", drawer.Importance)
	}
	if drawer.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
}

func TestCheckDuplicate_EmptyCandidates(t *testing.T) {
	store := newTestStore(t)
	ds := NewDrawerService(store, nil, DefaultConfig())
	ctx := context.Background()

	// No drawers in store - CheckDuplicate with nil embedder returns nil
	result, err := ds.CheckDuplicate(ctx, "some content", DrawerFilter{})
	if err != nil {
		t.Fatalf("CheckDuplicate: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nil embedder")
	}
}

func TestSearch_NilEmbedderFallback(t *testing.T) {
	store := newTestStore(t)
	insertTestDrawers(t, store)

	ds := NewDrawerService(store, nil, DefaultConfig())
	ctx := context.Background()

	// Verify FTS5 fallback works when embedder is nil
	results, err := ds.Search(ctx, SearchQuery{Query: "language", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	// Should find language-related content (Go, Python)
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results for 'language', got %d", len(results))
	}

	// Results with non-zero rank should have similarity scores
	for _, r := range results {
		if r.Rank != 0 && (r.Similarity <= 0 || r.Similarity > 1) {
			t.Errorf("unexpected similarity score for rank=%d: %.3f", r.Rank, r.Similarity)
		}
	}
}

func TestSearch_FTS5Fails(t *testing.T) {
	store := newTestStore(t)
	ds := NewDrawerService(store, nil, DefaultConfig())
	ctx := context.Background()

	// Search with no drawers - FTS5 returns empty but no error
	results, err := ds.Search(ctx, SearchQuery{Query: "test", Limit: 5})
	if err != nil {
		t.Fatalf("Search should not error on empty results: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for empty store, got %d", len(results))
	}
}

func TestAddDrawer_DeterministicID(t *testing.T) {
	ds := newTestDrawerService(t, nil)
	ctx := context.Background()

	input := DrawerInput{
		Wing:       "backend",
		Room:       "auth",
		Content:    "Same content",
		SourceFile: "auth.go",
		ChunkIndex: 0,
	}

	drawer1, err := ds.AddDrawer(ctx, input)
	if err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}

	// Same input generates same ID — INSERT OR REPLACE upserts silently
	drawer2, err := ds.AddDrawer(ctx, input)
	if err != nil {
		t.Fatalf("expected upsert to succeed: %v", err)
	}
	if drawer1.ID != drawer2.ID {
		t.Errorf("IDs differ: %q vs %q", drawer1.ID, drawer2.ID)
	}

	// Verify the drawer's ID is deterministic
	expectedID := GenerateDrawerID(input.Wing, input.Room, input.SourceFile, input.ChunkIndex, input.Content)
	if drawer1.ID != expectedID {
		t.Errorf("ID = %q, want %q", drawer1.ID, expectedID)
	}
}

// Exact duplicates must be rejected before the embedder is consulted — that
// ordering is the whole point of the content-hash check, since embedding is the
// expensive half of AddDrawer.
func TestAddDrawer_ExactDuplicateSkipsEmbedding(t *testing.T) {
	ctx := t.Context()
	counter := &countingEmbedder{dim: 8}
	ds := newTestDrawerService(t, counter)

	first := DrawerInput{Wing: "w", Room: "r", Content: "identical body", SourceFile: "a.go"}
	if _, err := ds.AddDrawer(ctx, first); err != nil {
		t.Fatalf("first AddDrawer: %v", err)
	}
	afterFirst := counter.calls

	// Same content, different source file => different id => a real duplicate.
	second := DrawerInput{Wing: "w", Room: "r", Content: "identical body", SourceFile: "b.go"}
	_, err := ds.AddDrawer(ctx, second)

	var dup *DuplicateError
	if !errors.As(err, &dup) {
		t.Fatalf("err = %v, want DuplicateError", err)
	}
	if counter.calls != afterFirst {
		t.Errorf("embedder called %d extra times; exact duplicates must short-circuit before embedding",
			counter.calls-afterFirst)
	}
}

// countingEmbedder records how many times Embed was called.
type countingEmbedder struct {
	dim   int
	calls int
}

func (c *countingEmbedder) Embed(texts []string) ([][]float32, error) {
	c.calls++
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, c.dim)
		out[i][0] = 1
	}
	return out, nil
}

func (c *countingEmbedder) Close() {}
