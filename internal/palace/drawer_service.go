package palace

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// DrawerService combines a PalaceStore and an optional Embedder to provide
// drawer CRUD with automatic embedding generation and duplicate detection.
type DrawerService struct {
	store    PalaceStore
	embedder Embedder
	config   PalaceConfig
	cache    *embeddingCache
}

// NewDrawerService creates a DrawerService. The embedder may be nil, in which
// case drawers are stored without embeddings and deduplication is skipped.
func NewDrawerService(store PalaceStore, embedder Embedder, config PalaceConfig) *DrawerService {
	return &DrawerService{
		store:    store,
		embedder: embedder,
		config:   config,
		cache:    newEmbeddingCache(),
	}
}

// candidates returns the embeddings matching filter, served from the in-process
// cache when possible. See embeddingCache for why this exists.
func (ds *DrawerService) candidates(ctx context.Context, filter DrawerFilter) ([]EmbeddingRow, error) {
	return ds.cache.get(ctx, filter, ds.store.GetAllEmbeddings)
}

// AddDrawer embeds the content, checks for duplicates in the same wing+room,
// generates an ID, and inserts the drawer. Returns an error wrapping a
// DuplicateResult if a near-duplicate is found.
func (ds *DrawerService) AddDrawer(ctx context.Context, input DrawerInput) (*Drawer, error) {
	if input.Content == "" {
		return nil, fmt.Errorf("palace: drawer content must not be empty")
	}
	if input.Wing == "" {
		return nil, fmt.Errorf("palace: drawer wing must not be empty")
	}
	if input.Room == "" {
		return nil, fmt.Errorf("palace: drawer room must not be empty")
	}

	id := GenerateDrawerID(input.Wing, input.Room, input.SourceFile, input.ChunkIndex, input.Content)

	// Exact duplicates are settled before embedding, not after. Embedding is the
	// expensive half of this function, so paying for it and then discovering the
	// content is byte-identical to something already stored is the worst possible
	// ordering. The lookup is an indexed hit on (wing, room, content_hash).
	//
	// A match on the drawer's *own* id is not a duplicate: ids are derived from
	// (source_file, chunk_index), so re-adding unchanged content to the same slot
	// is an idempotent upsert and must keep succeeding.
	contentHash := HashContent(input.Content)
	if existingID, err := ds.store.FindByContentHash(ctx, input.Wing, input.Room, contentHash); err != nil {
		slog.Warn("palace: content-hash dedup lookup failed", "error", err)
	} else if existingID != "" && existingID != id {
		return nil, &DuplicateError{Result: DuplicateResult{ExistingID: existingID, Similarity: 1}}
	}

	var embedding []float32

	if ds.embedder != nil {
		vecs, err := ds.embedder.Embed([]string{input.Content})
		if err != nil {
			slog.Warn("palace: embedding failed, storing without embedding", "error", err)
		} else if len(vecs) > 0 {
			embedding = vecs[0]

			// Check for near-duplicates within the same wing+room.
			candidates, err := ds.candidates(ctx, DrawerFilter{
				Wing: input.Wing,
				Room: input.Room,
			})
			if err != nil {
				slog.Warn("palace: failed to fetch embeddings for dedup", "error", err)
			} else {
				dupes := FindDuplicates(embedding, candidates, ds.config.DeduplicationThreshold)
				if len(dupes) > 0 {
					return nil, &DuplicateError{Result: dupes[0]}
				}
			}
		}
	}

	drawer := &Drawer{
		ID:          id,
		Wing:        input.Wing,
		Room:        input.Room,
		Hall:        input.Hall,
		Content:     input.Content,
		SourceFile:  input.SourceFile,
		ChunkIndex:  input.ChunkIndex,
		AddedBy:     input.AddedBy,
		Importance:  input.Importance,
		Embedding:   embedding,
		ContentHash: contentHash,
		CreatedAt:   time.Now().UTC(),
	}

	if err := ds.store.InsertDrawer(ctx, drawer); err != nil {
		return nil, err
	}

	// Keep the cache warm rather than dropping it: the vector is already in hand,
	// so appending avoids ever reloading the full set.
	ds.cache.add(input.Wing, input.Room, id, embedding)

	return drawer, nil
}

// Search performs a combined search using both semantic vector similarity and
// FTS5 keyword matching. When the embedder is available, results from both
// methods are merged and deduplicated by drawer ID, with semantic results
// prioritized — the order is semantic-first, not score-first, so a
// keyword-only hit can carry a higher score than the semantic hit above it.
//
// The keyword half is best-effort: a failing FTS5 query degrades to no keyword
// hits. The semantic half is not, and a failure to read the stored embeddings
// fails the search.
func (ds *DrawerService) Search(ctx context.Context, q SearchQuery) ([]SearchResult, error) {
	if q.Query == "" {
		return nil, fmt.Errorf("palace: search query must not be empty")
	}
	if q.Limit <= 0 {
		q.Limit = 5
	}

	filter := DrawerFilter{
		Wing: q.Wing,
		Room: q.Room,
	}

	// Fetch FTS5 results (always available).
	ftsResults, err := ds.store.KeywordSearch(ctx, q.Query, filter, q.Limit*3)
	if err != nil {
		slog.Warn("palace: FTS5 search failed", "error", err)
		ftsResults = nil
	}

	if ds.embedder == nil {
		return keywordOnlyResults(ftsResults, q.Limit), nil
	}

	// A query with no usable vector degrades to the keyword hits exactly as
	// the store returned them: unscored, untrimmed, and not merged.
	vec, ok := ds.queryVector(q.Query)
	if !ok {
		return ftsResults, nil
	}

	semantic, err := ds.semanticResults(ctx, vec, filter, q.Limit*3)
	if err != nil {
		return nil, err
	}
	return mergeSearchResults(semantic, ftsResults, q.Limit), nil
}

// queryVector embeds the query text for the semantic half of Search. It
// reports false rather than an error for both of its failure modes, because
// both are recoverable: the caller degrades to keyword results instead of
// failing the search.
func (ds *DrawerService) queryVector(query string) ([]float32, bool) {
	vecs, err := ds.embedder.Embed([]string{query})
	if err != nil {
		slog.Warn("palace: search embedding failed, falling back to keyword", "error", err)
		return nil, false
	}
	if len(vecs) == 0 {
		return nil, false
	}
	return vecs[0], true
}

// semanticResults ranks the stored embeddings matching filter against vec and
// loads the drawer behind each hit, keeping rank order. A drawer that ranks but
// cannot be loaded is dropped rather than failing the search — the ranking came
// from the embeddings table and the row may since have gone.
func (ds *DrawerService) semanticResults(ctx context.Context, vec []float32, filter DrawerFilter, limit int) ([]SearchResult, error) {
	candidates, err := ds.candidates(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("palace: search get embeddings: %w", err)
	}

	ranked := RankBySimilarity(vec, candidates, limit)
	results := make([]SearchResult, 0, len(ranked))
	for _, sr := range ranked {
		drawer, err := ds.store.GetDrawer(ctx, sr.DrawerID)
		if err != nil {
			continue
		}
		results = append(results, SearchResult{
			Drawer:     *drawer,
			Similarity: sr.Similarity,
		})
	}
	return results, nil
}

// keywordOnlyResults scores and trims the keyword hits for the no-embedder
// path, in place.
//
// A hit at rank 0 keeps whatever Similarity it arrived with instead of being
// scored. That is the one place this path differs from mergeSearchResults,
// which scores every keyword hit including those at rank 0.
func keywordOnlyResults(results []SearchResult, limit int) []SearchResult {
	maxRank := maxKeywordRank(results)
	for i := range results {
		if results[i].Rank != 0 {
			results[i].Similarity = keywordSimilarity(results[i].Rank, maxRank)
		}
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// mergeSearchResults returns the semantic hits followed by the keyword hits
// they did not already cover, trimmed to limit.
//
// Semantic hits keep their true cosine similarity and their rank order;
// keyword-only hits follow with a boosted score. A drawer found by both sides
// appears once, with its cosine score, because the semantic pass claims the ID
// first.
func mergeSearchResults(semantic, keyword []SearchResult, limit int) []SearchResult {
	merged := make([]SearchResult, 0, len(semantic)+len(keyword))
	seen := make(map[string]bool, len(semantic)+len(keyword))

	for _, sr := range semantic {
		merged = append(merged, sr)
		seen[sr.Drawer.ID] = true
	}

	maxRank := maxKeywordRank(keyword)
	for _, kw := range keyword {
		if seen[kw.Drawer.ID] {
			continue
		}
		seen[kw.Drawer.ID] = true
		merged = append(merged, SearchResult{
			Drawer:     kw.Drawer,
			Similarity: keywordSimilarity(kw.Rank, maxRank),
			Rank:       kw.Rank,
		})
	}

	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// maxKeywordRank returns the largest FTS5 rank in results, floored at 1 so it
// is always safe to divide by.
//
// The floor is not a corner case. SQLite's bm25 rank is negative — more
// negative means more relevant — so the maximum is normally the floor itself,
// and keywordSimilarity then scores a negative rank above 1. That contradicts
// the 0-1 range SearchResult.Similarity documents, and is pinned as-is by
// TestDrawerServiceSearch_KeywordOnlyScoring rather than fixed here.
func maxKeywordRank(results []SearchResult) int {
	maxRank := 1
	for _, r := range results {
		if r.Rank > maxRank {
			maxRank = r.Rank
		}
	}
	return maxRank
}

// keywordSimilarity converts an FTS5 rank into a score in the same space as
// cosine similarity, so keyword and semantic hits can share one list: rank 0
// scores 1 and the worst observed rank scores 0.5. maxRank must come from
// maxKeywordRank, which guarantees a non-zero divisor.
func keywordSimilarity(rank, maxRank int) float32 {
	return float32(0.5 + (0.5 * float64(maxRank-rank) / float64(maxRank)))
}

// DeleteDrawer removes a drawer by ID.
func (ds *DrawerService) DeleteDrawer(ctx context.Context, id string) error {
	if err := ds.store.DeleteDrawer(ctx, id); err != nil {
		return err
	}
	// Removal cannot be expressed as an append, so the cached sets are dropped
	// and rebuilt lazily.
	ds.cache.invalidate()
	return nil
}

// CheckDuplicate embeds the content and checks for near-duplicates across
// all drawers (or within the given filter). Returns nil if no duplicate found
// or if the embedder is not available.
func (ds *DrawerService) CheckDuplicate(ctx context.Context, content string, filter DrawerFilter) (*DuplicateResult, error) {
	if ds.embedder == nil {
		return nil, nil
	}

	vecs, err := ds.embedder.Embed([]string{content})
	if err != nil {
		return nil, fmt.Errorf("palace: embed for dedup: %w", err)
	}
	if len(vecs) == 0 {
		return nil, nil
	}

	candidates, err := ds.candidates(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("palace: fetch embeddings: %w", err)
	}

	dupes := FindDuplicates(vecs[0], candidates, ds.config.DeduplicationThreshold)
	if len(dupes) > 0 {
		return &dupes[0], nil
	}
	return nil, nil
}

// DuplicateError is returned by AddDrawer when a near-duplicate is detected.
type DuplicateError struct {
	Result DuplicateResult
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("palace: duplicate detected (existing=%s, similarity=%.3f)",
		e.Result.ExistingID, e.Result.Similarity)
}
