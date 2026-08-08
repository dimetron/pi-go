package palace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fixedEmbedder returns a canned vector per text, so similarity is fully
// controlled by the test rather than by a model.
type fixedEmbedder struct {
	vecs map[string][]float32
	err  error
	// fallback is returned for any text not in vecs.
	fallback []float32
}

func (f *fixedEmbedder) Embed(texts []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([][]float32, 0, len(texts))
	for _, txt := range texts {
		if v, ok := f.vecs[txt]; ok {
			out = append(out, v)
			continue
		}
		out = append(out, f.fallback)
	}
	return out, nil
}

func (f *fixedEmbedder) Close() {}

// emptyEmbedder returns no vectors at all, the degenerate case both Search and
// CheckDuplicate have to survive.
type emptyEmbedder struct{}

func (emptyEmbedder) Embed([]string) ([][]float32, error) { return nil, nil }
func (emptyEmbedder) Close()                              {}

func newSearchService(t *testing.T, emb Embedder) *DrawerService {
	t.Helper()
	cfg := DefaultConfig()
	return NewDrawerService(newTestStore(t), emb, cfg)
}

func addDrawer(t *testing.T, ds *DrawerService, wing, room, content string) *Drawer {
	t.Helper()
	d, err := ds.AddDrawer(context.Background(), DrawerInput{
		Wing: wing, Room: room, Content: content, AddedBy: "test",
	})
	if err != nil {
		t.Fatalf("AddDrawer(%q): %v", content, err)
	}
	return d
}

func TestDrawerService_SearchRejectsEmptyQuery(t *testing.T) {
	ds := newSearchService(t, nil)

	if _, err := ds.Search(context.Background(), SearchQuery{}); err == nil {
		t.Fatal("Search with an empty query returned no error")
	}
}

func TestDrawerService_SearchWithoutEmbedderUsesKeywordsOnly(t *testing.T) {
	ds := newSearchService(t, nil)
	addDrawer(t, ds, "pi-go", "internal", "the bash supervisor reaps process groups")
	addDrawer(t, ds, "pi-go", "internal", "unrelated content about pancakes")

	got, err := ds.Search(context.Background(), SearchQuery{Query: "supervisor"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("keyword search returned nothing for a term that is present")
	}
	// Every keyword hit must carry a similarity score, since callers rank on it
	// and a zero would sort a real match to the bottom.
	for i, r := range got {
		if r.Rank != 0 && r.Similarity <= 0 {
			t.Errorf("result %d has rank %d but similarity %v, want a positive score", i, r.Rank, r.Similarity)
		}
	}
}

func TestDrawerService_SearchAppliesDefaultAndExplicitLimit(t *testing.T) {
	ds := newSearchService(t, nil)
	for range 9 {
		addDrawer(t, ds, "pi-go", "internal", "supervisor supervisor supervisor")
	}

	// Limit <= 0 falls back to 5 rather than returning everything.
	got, err := ds.Search(context.Background(), SearchQuery{Query: "supervisor"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) > 5 {
		t.Errorf("default limit returned %d results, want at most 5", len(got))
	}

	got, err = ds.Search(context.Background(), SearchQuery{Query: "supervisor", Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) > 2 {
		t.Errorf("explicit limit returned %d results, want at most 2", len(got))
	}
}

func TestDrawerService_SearchFallsBackToKeywordsWhenEmbeddingFails(t *testing.T) {
	// An embedder outage must degrade to keyword search, not fail the query.
	ds := newSearchService(t, &fixedEmbedder{err: errors.New("model down"), fallback: []float32{1, 0, 0}})
	addDrawer(t, ds, "pi-go", "internal", "the bash supervisor reaps process groups")

	got, err := ds.Search(context.Background(), SearchQuery{Query: "supervisor"})
	if err != nil {
		t.Fatalf("Search must degrade rather than fail: %v", err)
	}
	if len(got) == 0 {
		t.Error("fallback returned no keyword results")
	}
}

func TestDrawerService_SearchSurvivesEmbedderReturningNoVectors(t *testing.T) {
	ds := newSearchService(t, emptyEmbedder{})
	addDrawer(t, ds, "pi-go", "internal", "the bash supervisor reaps process groups")

	got, err := ds.Search(context.Background(), SearchQuery{Query: "supervisor"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) == 0 {
		t.Error("expected the keyword results to survive an empty embedding")
	}
}

func TestDrawerService_CheckDuplicateWithoutEmbedderIsSkipped(t *testing.T) {
	ds := newSearchService(t, nil)

	got, err := ds.CheckDuplicate(context.Background(), "anything", DrawerFilter{})
	if err != nil {
		t.Fatalf("CheckDuplicate: %v", err)
	}
	if got != nil {
		t.Errorf("CheckDuplicate without an embedder = %+v, want nil (skipped)", got)
	}
}

func TestDrawerService_CheckDuplicateFindsIdenticalVector(t *testing.T) {
	same := []float32{1, 0, 0}
	emb := &fixedEmbedder{fallback: same}
	ds := newSearchService(t, emb)

	existing := addDrawer(t, ds, "pi-go", "internal", "original content")

	// The candidate embeds to the same vector, so cosine similarity is 1.0 —
	// comfortably over the 0.9 default threshold.
	got, err := ds.CheckDuplicate(context.Background(), "original content", DrawerFilter{})
	if err != nil {
		t.Fatalf("CheckDuplicate: %v", err)
	}
	if got == nil {
		t.Fatal("an identical vector was not reported as a duplicate")
	}
	if got.ExistingID != existing.ID {
		t.Errorf("ExistingID = %q, want %q", got.ExistingID, existing.ID)
	}
	if got.Similarity < ds.config.DeduplicationThreshold {
		t.Errorf("Similarity = %v, want >= the %v threshold", got.Similarity, ds.config.DeduplicationThreshold)
	}
}

func TestDrawerService_CheckDuplicateIgnoresDistantVector(t *testing.T) {
	emb := &fixedEmbedder{
		vecs: map[string][]float32{
			"original content": {1, 0, 0},
			"nothing alike":    {0, 1, 0}, // orthogonal => similarity 0
		},
	}
	ds := newSearchService(t, emb)
	addDrawer(t, ds, "pi-go", "internal", "original content")

	got, err := ds.CheckDuplicate(context.Background(), "nothing alike", DrawerFilter{})
	if err != nil {
		t.Fatalf("CheckDuplicate: %v", err)
	}
	if got != nil {
		t.Errorf("an orthogonal vector was reported as a duplicate: %+v", got)
	}
}

func TestDrawerService_CheckDuplicatePropagatesEmbedError(t *testing.T) {
	// Unlike Search, a dedup check that cannot embed must not silently report
	// "no duplicate" — that would let the caller write a duplicate.
	ds := newSearchService(t, &fixedEmbedder{err: errors.New("model down")})

	_, err := ds.CheckDuplicate(context.Background(), "content", DrawerFilter{})
	if err == nil {
		t.Fatal("CheckDuplicate swallowed an embedding failure")
	}
}

func TestDrawerService_CheckDuplicateSurvivesEmptyEmbedding(t *testing.T) {
	ds := newSearchService(t, emptyEmbedder{})

	got, err := ds.CheckDuplicate(context.Background(), "content", DrawerFilter{})
	if err != nil {
		t.Fatalf("CheckDuplicate: %v", err)
	}
	if got != nil {
		t.Errorf("got %+v, want nil when the embedder returns no vector", got)
	}
}

func TestDuplicateError_MessageNamesTheExistingDrawer(t *testing.T) {
	err := &DuplicateError{Result: DuplicateResult{ExistingID: "drw_123", Similarity: 0.987}}

	msg := err.Error()
	if !strings.Contains(msg, "drw_123") || !strings.Contains(msg, "0.987") {
		t.Errorf("Error() = %q, want it to name the existing drawer and similarity", msg)
	}
}
