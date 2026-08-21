package palace

import (
	"context"
	"errors"
	"testing"
)

// scriptedStore wraps a real store so one method at a time can be made to fail
// or return canned rows. Embedding PalaceStore keeps the ~25 methods this test
// does not care about delegating to the real SQLite store.
type scriptedStore struct {
	PalaceStore
	keywordResults []SearchResult
	keywordErr     error
	embeddingsErr  error
	getDrawerErr   map[string]error
}

func (s *scriptedStore) KeywordSearch(ctx context.Context, query string, filter DrawerFilter, limit int) ([]SearchResult, error) {
	if s.keywordErr != nil {
		return nil, s.keywordErr
	}
	if s.keywordResults != nil {
		return s.keywordResults, nil
	}
	return s.PalaceStore.KeywordSearch(ctx, query, filter, limit)
}

func (s *scriptedStore) GetAllEmbeddings(ctx context.Context, filter DrawerFilter) ([]EmbeddingRow, error) {
	if s.embeddingsErr != nil {
		return nil, s.embeddingsErr
	}
	return s.PalaceStore.GetAllEmbeddings(ctx, filter)
}

func (s *scriptedStore) GetDrawer(ctx context.Context, id string) (*Drawer, error) {
	if err, ok := s.getDrawerErr[id]; ok {
		return nil, err
	}
	return s.PalaceStore.GetDrawer(ctx, id)
}

// searchFixture builds a store holding two embedded drawers (alpha, beta) and
// one unembedded drawer (gamma), so a test can tell semantic hits from
// keyword-only hits by ID alone.
//
// With a query vector of {1, 0}: alpha scores 1.0 and beta scores 0.6. Gamma
// has no embedding, so it can only ever arrive through the keyword side.
func searchFixture(t *testing.T) *scriptedStore {
	t.Helper()

	store := &scriptedStore{PalaceStore: newTestStore(t)}
	ctx := context.Background()

	alpha := makeDrawer("d_alpha", "tech", "go", "alpha content about goroutines")
	alpha.Embedding = []float32{1, 0}
	beta := makeDrawer("d_beta", "tech", "go", "beta content about goroutines")
	beta.Embedding = []float32{0.6, 0.8}
	gamma := makeDrawer("d_gamma", "tech", "go", "gamma content about goroutines")

	for _, d := range []*Drawer{alpha, beta, gamma} {
		if err := store.InsertDrawer(ctx, d); err != nil {
			t.Fatalf("InsertDrawer(%s): %v", d.ID, err)
		}
	}
	return store
}

// queryEmbedder answers every Embed call with {1, 0}, the vector searchFixture
// documents its similarity scores against.
func queryEmbedder() Embedder {
	return &fixedEmbedder{fallback: []float32{1, 0}}
}

func resultIDs(results []SearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.Drawer.ID)
	}
	return ids
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func closeEnough(got, want float32) bool {
	const tolerance = 1e-6
	d := got - want
	return d < tolerance && d > -tolerance
}

// TestDrawerServiceSearch_KeywordOnlyScoring pins the score the keyword-only
// path (no embedder) writes onto each result.
//
// Two things are load-bearing and easy to lose in a refactor. A result whose
// Rank is 0 keeps whatever Similarity it arrived with — it is not scored at
// all. And the normalizer's denominator floors at 1, never at the observed
// rank, so the negative ranks SQLite's bm25 actually produces push the score
// above 1 rather than clamping to it.
func TestDrawerServiceSearch_KeywordOnlyScoring(t *testing.T) {
	tests := []struct {
		name  string
		ranks []int
		want  []float32
	}{
		{
			name:  "rank zero is left unscored",
			ranks: []int{0, 0},
			want:  []float32{0, 0},
		},
		{
			name:  "positive ranks normalize against the largest",
			ranks: []int{1, 2, 4},
			want:  []float32{0.875, 0.75, 0.5},
		},
		{
			name:  "negative ranks score above one because the divisor floors at 1",
			ranks: []int{-1, -3, 0},
			want:  []float32{1.5, 2.5, 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &scriptedStore{PalaceStore: newTestStore(t)}
			for i, rank := range tt.ranks {
				store.keywordResults = append(store.keywordResults, SearchResult{
					Drawer: *makeDrawer(string(rune('a'+i)), "tech", "go", "content"),
					Rank:   rank,
				})
			}

			ds := NewDrawerService(store, nil, DefaultConfig())
			got, err := ds.Search(context.Background(), SearchQuery{Query: "content", Limit: 10})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d results, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if !closeEnough(got[i].Similarity, tt.want[i]) {
					t.Errorf("result %d (rank %d) similarity = %v, want %v",
						i, tt.ranks[i], got[i].Similarity, tt.want[i])
				}
			}
		})
	}
}

func TestDrawerServiceSearch_KeywordOnlyTrimsToLimit(t *testing.T) {
	store := &scriptedStore{PalaceStore: newTestStore(t)}
	for i := range 6 {
		store.keywordResults = append(store.keywordResults, SearchResult{
			Drawer: *makeDrawer(string(rune('a'+i)), "tech", "go", "content"),
			Rank:   i + 1,
		})
	}

	ds := NewDrawerService(store, nil, DefaultConfig())
	got, err := ds.Search(context.Background(), SearchQuery{Query: "content", Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !equalIDs(resultIDs(got), []string{"a", "b"}) {
		t.Errorf("got %v, want the first two keyword results", resultIDs(got))
	}
}

// TestDrawerServiceSearch_MergeOrdersSemanticFirst pins the merge order, which
// is not the score order: semantic hits come first in similarity order, then
// keyword hits the semantic pass did not already produce — even when the
// keyword hit's boosted score is higher.
func TestDrawerServiceSearch_MergeOrdersSemanticFirst(t *testing.T) {
	store := searchFixture(t)
	beta, err := store.GetDrawer(context.Background(), "d_beta")
	if err != nil {
		t.Fatalf("GetDrawer: %v", err)
	}
	gamma, err := store.GetDrawer(context.Background(), "d_gamma")
	if err != nil {
		t.Fatalf("GetDrawer: %v", err)
	}
	// beta is also a semantic hit and must not be duplicated; gamma is
	// keyword-only.
	store.keywordResults = []SearchResult{
		{Drawer: *gamma, Rank: 0},
		{Drawer: *beta, Rank: 0},
	}

	ds := NewDrawerService(store, queryEmbedder(), DefaultConfig())
	got, err := ds.Search(context.Background(), SearchQuery{Query: "goroutines", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if !equalIDs(resultIDs(got), []string{"d_alpha", "d_beta", "d_gamma"}) {
		t.Fatalf("got %v, want [d_alpha d_beta d_gamma]", resultIDs(got))
	}
	if !closeEnough(got[0].Similarity, 1) {
		t.Errorf("d_alpha similarity = %v, want 1 (identical vector)", got[0].Similarity)
	}
	if !closeEnough(got[1].Similarity, 0.6) {
		t.Errorf("d_beta similarity = %v, want 0.6 (cosine, not the keyword boost)", got[1].Similarity)
	}
	if !closeEnough(got[2].Similarity, 1) {
		t.Errorf("d_gamma similarity = %v, want 1 (keyword boost at rank 0)", got[2].Similarity)
	}
}

func TestDrawerServiceSearch_MergeTrimsToLimit(t *testing.T) {
	store := searchFixture(t)
	gamma, err := store.GetDrawer(context.Background(), "d_gamma")
	if err != nil {
		t.Fatalf("GetDrawer: %v", err)
	}
	store.keywordResults = []SearchResult{{Drawer: *gamma, Rank: 0}}

	ds := NewDrawerService(store, queryEmbedder(), DefaultConfig())
	got, err := ds.Search(context.Background(), SearchQuery{Query: "goroutines", Limit: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !equalIDs(resultIDs(got), []string{"d_alpha", "d_beta"}) {
		t.Errorf("got %v, want the two semantic hits and no room for the keyword hit", resultIDs(got))
	}
}

// TestDrawerServiceSearch_DropsRankedDrawersThatWillNotLoad pins the quiet
// skip: a drawer that ranks but cannot be fetched is dropped from the results
// rather than failing the search.
func TestDrawerServiceSearch_DropsRankedDrawersThatWillNotLoad(t *testing.T) {
	store := searchFixture(t)
	store.getDrawerErr = map[string]error{"d_alpha": errors.New("boom")}
	store.keywordResults = []SearchResult{}

	ds := NewDrawerService(store, queryEmbedder(), DefaultConfig())
	got, err := ds.Search(context.Background(), SearchQuery{Query: "goroutines", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !equalIDs(resultIDs(got), []string{"d_beta"}) {
		t.Errorf("got %v, want [d_beta] with the unloadable d_alpha dropped", resultIDs(got))
	}
}

// TestDrawerServiceSearch_ContinuesWhenKeywordSearchFails pins the asymmetry: a
// keyword failure is downgraded to "no keyword hits" and the semantic half
// still runs, where an embeddings-fetch failure fails the whole search.
func TestDrawerServiceSearch_ContinuesWhenKeywordSearchFails(t *testing.T) {
	store := searchFixture(t)
	store.keywordErr = errors.New("fts is down")

	ds := NewDrawerService(store, queryEmbedder(), DefaultConfig())
	got, err := ds.Search(context.Background(), SearchQuery{Query: "goroutines", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !equalIDs(resultIDs(got), []string{"d_alpha", "d_beta"}) {
		t.Errorf("got %v, want the semantic hits only", resultIDs(got))
	}
}

func TestDrawerServiceSearch_FailsWhenEmbeddingsCannotBeFetched(t *testing.T) {
	store := searchFixture(t)
	store.embeddingsErr = errors.New("disk gone")

	ds := NewDrawerService(store, queryEmbedder(), DefaultConfig())
	_, err := ds.Search(context.Background(), SearchQuery{Query: "goroutines", Limit: 10})
	if err == nil {
		t.Fatal("Search: want an error when embeddings cannot be fetched")
	}
	if !errors.Is(err, store.embeddingsErr) {
		t.Errorf("Search error = %v, want it to wrap the store error", err)
	}
}

func TestDrawerServiceSearch_DefaultsLimitToFive(t *testing.T) {
	store := &scriptedStore{PalaceStore: newTestStore(t)}
	for i := range 8 {
		store.keywordResults = append(store.keywordResults, SearchResult{
			Drawer: *makeDrawer(string(rune('a'+i)), "tech", "go", "content"),
			Rank:   i + 1,
		})
	}

	ds := NewDrawerService(store, nil, DefaultConfig())
	got, err := ds.Search(context.Background(), SearchQuery{Query: "content"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 5 {
		t.Errorf("got %d results, want the default limit of 5", len(got))
	}
}

// TestDrawerServiceSearch_MergeNormalizesKeywordRanks pins the second, separate
// rank normalization — the one applied to keyword hits that survive the merge.
// It uses the same formula as the keyword-only path but scores every hit,
// including those at rank 0.
func TestDrawerServiceSearch_MergeNormalizesKeywordRanks(t *testing.T) {
	store := searchFixture(t)
	// Neither drawer is in the store: the merge path reads FTS rows straight
	// through without a lookup, which is itself worth pinning.
	store.keywordResults = []SearchResult{
		{Drawer: *makeDrawer("d_delta", "tech", "go", "delta"), Rank: 1},
		{Drawer: *makeDrawer("d_epsilon", "tech", "go", "epsilon"), Rank: 4},
	}

	ds := NewDrawerService(store, queryEmbedder(), DefaultConfig())
	got, err := ds.Search(context.Background(), SearchQuery{Query: "goroutines", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !equalIDs(resultIDs(got), []string{"d_alpha", "d_beta", "d_delta", "d_epsilon"}) {
		t.Fatalf("got %v", resultIDs(got))
	}
	// maxRank is 4, so rank 1 scores 0.5+0.5*3/4 and rank 4 scores 0.5.
	if !closeEnough(got[2].Similarity, 0.875) {
		t.Errorf("d_delta similarity = %v, want 0.875", got[2].Similarity)
	}
	if !closeEnough(got[3].Similarity, 0.5) {
		t.Errorf("d_epsilon similarity = %v, want 0.5", got[3].Similarity)
	}
	if got[2].Rank != 1 || got[3].Rank != 4 {
		t.Errorf("merged keyword hits lost their Rank: %d, %d", got[2].Rank, got[3].Rank)
	}
}

func TestMaxKeywordRank(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		ranks []int
		want  int
	}{
		{name: "no results floors at 1", ranks: nil, want: 1},
		{name: "all zero floors at 1", ranks: []int{0, 0}, want: 1},
		{name: "all negative floors at 1", ranks: []int{-1, -7, -3}, want: 1},
		{name: "largest positive wins", ranks: []int{1, 9, 4}, want: 9},
		{name: "mixed signs take the positive max", ranks: []int{-5, 3, 0}, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			results := make([]SearchResult, 0, len(tt.ranks))
			for _, rank := range tt.ranks {
				results = append(results, SearchResult{Rank: rank})
			}
			if got := maxKeywordRank(results); got != tt.want {
				t.Errorf("maxKeywordRank(%v) = %d, want %d", tt.ranks, got, tt.want)
			}
		})
	}
}

func TestKeywordSimilarity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rank    int
		maxRank int
		want    float32
	}{
		{name: "rank zero is the best score", rank: 0, maxRank: 4, want: 1},
		{name: "the worst rank is the floor score", rank: 4, maxRank: 4, want: 0.5},
		{name: "midway", rank: 2, maxRank: 4, want: 0.75},
		{name: "single result at rank 1", rank: 1, maxRank: 1, want: 0.5},
		{name: "negative rank exceeds 1", rank: -1, maxRank: 1, want: 1.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := keywordSimilarity(tt.rank, tt.maxRank); !closeEnough(got, tt.want) {
				t.Errorf("keywordSimilarity(%d, %d) = %v, want %v", tt.rank, tt.maxRank, got, tt.want)
			}
		})
	}
}

func TestMergeSearchResults(t *testing.T) {
	t.Parallel()

	hit := func(id string, sim float32, rank int) SearchResult {
		return SearchResult{Drawer: Drawer{ID: id}, Similarity: sim, Rank: rank}
	}

	tests := []struct {
		name     string
		semantic []SearchResult
		keyword  []SearchResult
		limit    int
		wantIDs  []string
	}{
		{
			name:    "keyword only",
			keyword: []SearchResult{hit("a", 0, 1), hit("b", 0, 2)},
			limit:   5,
			wantIDs: []string{"a", "b"},
		},
		{
			name:     "semantic only",
			semantic: []SearchResult{hit("a", 0.9, 0)},
			limit:    5,
			wantIDs:  []string{"a"},
		},
		{
			name:     "semantic comes first regardless of score",
			semantic: []SearchResult{hit("a", 0.1, 0)},
			keyword:  []SearchResult{hit("b", 0, 0)},
			limit:    5,
			wantIDs:  []string{"a", "b"},
		},
		{
			name:     "a drawer on both sides appears once",
			semantic: []SearchResult{hit("a", 0.9, 0)},
			keyword:  []SearchResult{hit("a", 0, 1), hit("b", 0, 2)},
			limit:    5,
			wantIDs:  []string{"a", "b"},
		},
		{
			name:    "a drawer repeated on the keyword side appears once",
			keyword: []SearchResult{hit("a", 0, 1), hit("a", 0, 2)},
			limit:   5,
			wantIDs: []string{"a"},
		},
		{
			name:     "limit trims from the tail, dropping keyword hits first",
			semantic: []SearchResult{hit("a", 0.9, 0), hit("b", 0.8, 0)},
			keyword:  []SearchResult{hit("c", 0, 1)},
			limit:    2,
			wantIDs:  []string{"a", "b"},
		},
		{
			name:    "empty input yields an empty, non-nil slice",
			limit:   5,
			wantIDs: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mergeSearchResults(tt.semantic, tt.keyword, tt.limit)
			if got == nil {
				t.Fatal("mergeSearchResults returned nil, want an empty slice")
			}
			if !equalIDs(resultIDs(got), tt.wantIDs) {
				t.Errorf("got %v, want %v", resultIDs(got), tt.wantIDs)
			}
		})
	}
}

// TestMergeSearchResults_KeepsSemanticScores pins that merging does not
// re-score a semantic hit with the keyword formula when the same drawer also
// came back from FTS5.
func TestMergeSearchResults_KeepsSemanticScores(t *testing.T) {
	t.Parallel()

	semantic := []SearchResult{{Drawer: Drawer{ID: "a"}, Similarity: 0.42}}
	keyword := []SearchResult{{Drawer: Drawer{ID: "a"}, Rank: 3}}

	got := mergeSearchResults(semantic, keyword, 5)
	if len(got) != 1 {
		t.Fatalf("got %d results, want 1", len(got))
	}
	if !closeEnough(got[0].Similarity, 0.42) {
		t.Errorf("similarity = %v, want the cosine score 0.42", got[0].Similarity)
	}
	if got[0].Rank != 0 {
		t.Errorf("rank = %d, want 0: the semantic hit's copy has no FTS rank", got[0].Rank)
	}
}
