package palace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// Palace is the top-level facade that ties all palace components together.
type Palace struct {
	store    PalaceStore
	embedder Embedder
	drawers  *DrawerService
	graph    *Graph
	kg       *KnowledgeGraph
	layers   *MemoryStack
	config   PalaceConfig
}

// New creates a Palace, opening the database, optionally loading the embedder,
// and wiring all components together. Functional options configure the behavior.
func New(opts ...Option) (*Palace, error) {
	cfg := DefaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	db, err := OpenDB(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("palace: open db: %w", err)
	}

	store := NewSQLitePalaceStore(db)

	embedder := openEmbedder(cfg)

	p := &Palace{
		store:    store,
		embedder: embedder,
		drawers:  NewDrawerService(store, embedder, cfg),
		graph:    NewGraph(store),
		kg:       NewKnowledgeGraph(store),
		layers:   NewMemoryStack(store, embedder, cfg),
		config:   cfg,
	}
	return p, nil
}

// openEmbedder resolves the configured embedding backend.
//
// Ollama is tried first when enabled, then the in-process model. Both are
// optional: a palace with no embedder still serves FTS5 keyword search, which is
// why every failure here is a warning rather than an error. Callers that cannot
// work without embeddings — mining — must check availability themselves and say
// something useful; see EmbedderAvailability.
func openEmbedder(cfg PalaceConfig) Embedder {
	if cfg.UseOllama {
		e, err := NewOllamaEmbedder(cfg.OllamaURL, cfg.OllamaModel)
		if err == nil {
			return e
		}
		slog.Warn("palace: ollama embedder unavailable, falling back to in-process model",
			"error", err, "url", cfg.OllamaURL, "model", cfg.OllamaModel)
	}

	if cfg.ModelPath == "" {
		return nil
	}
	if _, err := os.Stat(cfg.ModelPath); err != nil {
		return nil
	}
	e, err := NewEmbedder(cfg.ModelPath)
	if err != nil {
		slog.Warn("palace: failed to load embedder, continuing without", "error", err)
		return nil
	}
	return e
}

// EmbedderAvailability reports whether the configured embedding backend can be
// reached, and why not when it cannot. It performs the same checks New would,
// without building a palace, so commands that require embeddings can fail early
// with an actionable message.
func EmbedderAvailability(cfg PalaceConfig) error {
	if !cfg.UseOllama {
		if cfg.ModelPath == "" {
			return fmt.Errorf("no embedding model configured")
		}
		if _, err := os.Stat(cfg.ModelPath); err != nil {
			return fmt.Errorf("embedding model not found at %s", cfg.ModelPath)
		}
		return nil
	}
	if _, err := NewOllamaEmbedder(cfg.OllamaURL, cfg.OllamaModel); err != nil {
		return err
	}
	return nil
}

// NewWithStore creates a Palace using the provided store and optional embedder.
// This is useful for testing with in-memory stores.
func NewWithStore(store PalaceStore, embedder Embedder, opts ...Option) *Palace {
	cfg := DefaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return &Palace{
		store:    store,
		embedder: embedder,
		drawers:  NewDrawerService(store, embedder, cfg),
		graph:    NewGraph(store),
		kg:       NewKnowledgeGraph(store),
		layers:   NewMemoryStack(store, embedder, cfg),
		config:   cfg,
	}
}

// --- Drawer operations ---

// AddDrawer embeds content, checks for duplicates, and stores a new drawer.
func (p *Palace) AddDrawer(ctx context.Context, input DrawerInput) (*Drawer, error) {
	return p.drawers.AddDrawer(ctx, input)
}

// DeleteDrawer removes a drawer by ID.
func (p *Palace) DeleteDrawer(ctx context.Context, id string) error {
	return p.drawers.DeleteDrawer(ctx, id)
}

// GetDrawer retrieves a drawer by ID.
func (p *Palace) GetDrawer(ctx context.Context, id string) (*Drawer, error) {
	return p.store.GetDrawer(ctx, id)
}

// ListDrawers returns drawers matching the given filter.
func (p *Palace) ListDrawers(ctx context.Context, filter DrawerFilter) ([]*Drawer, error) {
	return p.store.ListDrawers(ctx, filter)
}

// --- Search ---

// Search performs semantic or FTS5 keyword search.
func (p *Palace) Search(ctx context.Context, q SearchQuery) ([]SearchResult, error) {
	return p.drawers.Search(ctx, q)
}

// --- Hierarchy ---

// ListWings returns aggregate wing summaries.
func (p *Palace) ListWings(ctx context.Context) ([]WingSummary, error) {
	return p.store.ListWings(ctx)
}

// ListRooms returns rooms within a wing.
func (p *Palace) ListRooms(ctx context.Context, wing string) ([]RoomSummary, error) {
	return p.store.ListRooms(ctx, wing)
}

// GetTaxonomy returns the full wing → room hierarchy.
func (p *Palace) GetTaxonomy(ctx context.Context) (*Taxonomy, error) {
	return p.store.GetTaxonomy(ctx)
}

// --- Graph ---

// Traverse performs BFS traversal from a starting room.
func (p *Palace) Traverse(ctx context.Context, startRoom string, maxHops int) ([]TraverseResult, error) {
	return p.graph.Traverse(ctx, startRoom, maxHops)
}

// FindTunnels returns rooms that bridge multiple wings.
func (p *Palace) FindTunnels(ctx context.Context, wingA, wingB string) ([]Tunnel, error) {
	return p.graph.FindTunnels(ctx, wingA, wingB)
}

// GraphStats returns palace graph statistics.
func (p *Palace) GraphStats(ctx context.Context) (*GraphStats, error) {
	return p.graph.Stats(ctx)
}

// --- Knowledge Graph ---

// KGAdd creates a triple, auto-creating entities if needed.
func (p *Palace) KGAdd(ctx context.Context, input TripleInput) (*Triple, error) {
	return p.kg.Add(ctx, input)
}

// KGQuery returns triples involving the named entity.
func (p *Palace) KGQuery(ctx context.Context, entity, asOf, direction string) ([]*Triple, error) {
	return p.kg.Query(ctx, entity, asOf, direction)
}

// KGInvalidate sets valid_to on an active triple.
func (p *Palace) KGInvalidate(ctx context.Context, subject, predicate, object string) error {
	return p.kg.Invalidate(ctx, subject, predicate, object)
}

// KGTimeline returns all triples for an entity ordered chronologically.
func (p *Palace) KGTimeline(ctx context.Context, entity string) ([]*Triple, error) {
	return p.kg.Timeline(ctx, entity)
}

// KGStats returns knowledge graph statistics.
func (p *Palace) KGStats(ctx context.Context) (*KGStats, error) {
	return p.kg.Stats(ctx)
}

// --- Memory Layers ---

// WakeUp returns the combined L0 + L1 context string.
func (p *Palace) WakeUp(ctx context.Context, wing string) (string, error) {
	return p.layers.WakeUp(ctx, wing)
}

// Recall returns L2 on-demand context for a specific wing and room.
func (p *Palace) Recall(ctx context.Context, wing, room string) (string, error) {
	return p.layers.Recall(ctx, wing, room)
}

// --- Diary ---

// DiaryWrite stores a diary entry for an agent.
func (p *Palace) DiaryWrite(ctx context.Context, agent, entry, topic string) error {
	return p.store.InsertDiaryEntry(ctx, &DiaryEntry{
		Agent: agent,
		Entry: entry,
		Topic: topic,
	})
}

// DiaryRead returns recent diary entries for an agent.
func (p *Palace) DiaryRead(ctx context.Context, agent string, lastN int) ([]*DiaryEntry, error) {
	return p.store.ListDiaryEntries(ctx, agent, lastN)
}

// --- Status ---

// Status returns an aggregate view of the palace.
func (p *Palace) Status(ctx context.Context) (*PalaceStatus, error) {
	count, err := p.store.CountDrawers(ctx)
	if err != nil {
		return nil, fmt.Errorf("palace: status count: %w", err)
	}

	wings, err := p.store.ListWings(ctx)
	if err != nil {
		return nil, fmt.Errorf("palace: status wings: %w", err)
	}

	var totalRooms int
	for _, w := range wings {
		totalRooms += w.RoomCount
	}

	kgStats, err := p.kg.Stats(ctx)
	if err != nil {
		slog.Warn("palace: status kg stats", "error", err)
	}

	return &PalaceStatus{
		DrawerCount: count,
		WingCount:   len(wings),
		RoomCount:   totalRooms,
		KG:          kgStats,
		ModelLoaded: p.embedder != nil,
	}, nil
}

// --- Lifecycle ---

// Close releases all palace resources.
func (p *Palace) Close() error {
	if p.embedder != nil {
		p.embedder.Close()
	}
	return p.store.Close()
}
