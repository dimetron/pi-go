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
	embedder *Embedder
	config   PalaceConfig
}

// NewDrawerService creates a DrawerService. The embedder may be nil, in which
// case drawers are stored without embeddings and deduplication is skipped.
func NewDrawerService(store PalaceStore, embedder *Embedder, config PalaceConfig) *DrawerService {
	return &DrawerService{
		store:    store,
		embedder: embedder,
		config:   config,
	}
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

	var embedding []float32

	if ds.embedder != nil {
		vecs, err := ds.embedder.Embed([]string{input.Content})
		if err != nil {
			slog.Warn("palace: embedding failed, storing without embedding", "error", err)
		} else if len(vecs) > 0 {
			embedding = vecs[0]

			// Check for duplicates within the same wing+room.
			candidates, err := ds.store.GetAllEmbeddings(ctx, DrawerFilter{
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

	id := GenerateDrawerID(input.Wing, input.Room, input.SourceFile, input.ChunkIndex, input.Content)

	drawer := &Drawer{
		ID:         id,
		Wing:       input.Wing,
		Room:       input.Room,
		Hall:       input.Hall,
		Content:    input.Content,
		SourceFile: input.SourceFile,
		ChunkIndex: input.ChunkIndex,
		AddedBy:    input.AddedBy,
		Importance: input.Importance,
		Embedding:  embedding,
		CreatedAt:  time.Now().UTC(),
	}

	if err := ds.store.InsertDrawer(ctx, drawer); err != nil {
		return nil, err
	}

	return drawer, nil
}

// Search performs a combined search using both semantic vector similarity and
// FTS5 keyword matching. When the embedder is available, results from both methods
// are merged and deduplicated by drawer ID, with semantic results prioritized.
// Returns results sorted by combined relevance score.
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

	// If no embedder, return FTS5 results only.
	if ds.embedder == nil {
		// Ensure FTS5 results have proper similarity scores.
		maxRank := 1
		for _, r := range ftsResults {
			if r.Rank > maxRank {
				maxRank = r.Rank
			}
		}
		if maxRank == 0 {
			maxRank = 1
		}
		for i := range ftsResults {
			if ftsResults[i].Rank != 0 {
				ftsResults[i].Similarity = float32(0.5 + (0.5 * float64(maxRank-ftsResults[i].Rank) / float64(maxRank)))
			}
		}
		if len(ftsResults) > q.Limit {
			ftsResults = ftsResults[:q.Limit]
		}
		return ftsResults, nil
	}

	// Semantic search.
	vecs, err := ds.embedder.Embed([]string{q.Query})
	if err != nil {
		slog.Warn("palace: search embedding failed, falling back to keyword", "error", err)
		return ftsResults, nil
	}
	if len(vecs) == 0 {
		return ftsResults, nil
	}

	candidates, err := ds.store.GetAllEmbeddings(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("palace: search get embeddings: %w", err)
	}

	ranked := RankBySimilarity(vecs[0], candidates, q.Limit*3)

	// Build a map of semantic results keyed by drawer ID.
	semanticMap := make(map[string]SearchResult)
	for _, sr := range ranked {
		drawer, err := ds.store.GetDrawer(ctx, sr.DrawerID)
		if err != nil {
			continue
		}
		semanticMap[sr.DrawerID] = SearchResult{
			Drawer:     *drawer,
			Similarity: sr.Similarity,
		}
	}

	// Merge FTS5 results into semantic map.
	// FTS5 results get a boosted score: 0.5 + (0.5 * normalizedRank)
	// This ensures exact keyword matches rank high but semantic still matters.
	maxRank := 1
	for _, r := range ftsResults {
		if r.Rank > maxRank {
			maxRank = r.Rank
		}
	}
	if maxRank == 0 {
		maxRank = 1
	}

	merged := make([]SearchResult, 0, len(semanticMap)+len(ftsResults))
	seen := make(map[string]bool)

	// Add semantic results first (they have true similarity scores).
	for _, sr := range ranked {
		if result, ok := semanticMap[sr.DrawerID]; ok {
			merged = append(merged, result)
			seen[sr.DrawerID] = true
		}
	}

	// Add FTS5 results not already in semantic results.
	for _, fts := range ftsResults {
		if seen[fts.Drawer.ID] {
			continue
		}
		// Convert FTS5 rank to a relevance score (higher = better).
		// FTS5 rank is negative (more negative = more relevant), so we normalize.
		var ftsScore float64
		if maxRank > 0 {
			// Normalize: most negative = highest score
			ftsScore = 0.5 + (0.5 * float64(maxRank-fts.Rank) / float64(maxRank))
		} else {
			ftsScore = 0.5
		}
		merged = append(merged, SearchResult{
			Drawer:     fts.Drawer,
			Similarity: float32(ftsScore),
			Rank:       fts.Rank,
		})
		seen[fts.Drawer.ID] = true
	}

	// Trim to requested limit.
	if len(merged) > q.Limit {
		merged = merged[:q.Limit]
	}

	return merged, nil
}

// DeleteDrawer removes a drawer by ID.
func (ds *DrawerService) DeleteDrawer(ctx context.Context, id string) error {
	return ds.store.DeleteDrawer(ctx, id)
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

	candidates, err := ds.store.GetAllEmbeddings(ctx, filter)
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
