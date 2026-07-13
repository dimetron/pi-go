package palace

import "context"

// PalaceStore defines the interface for palace persistence operations.
type PalaceStore interface {
	// Drawer operations
	InsertDrawer(ctx context.Context, d *Drawer) error
	BatchInsertDrawers(ctx context.Context, drawers []*Drawer) (int, error)
	DeleteDrawer(ctx context.Context, id string) error
	GetDrawer(ctx context.Context, id string) (*Drawer, error)
	ListDrawers(ctx context.Context, filter DrawerFilter) ([]*Drawer, error)
	CountDrawers(ctx context.Context) (int, error)
	// DrawerHashes returns id → content_hash for a wing, so the miner can skip
	// re-embedding chunks whose content has not changed since the last run.
	DrawerHashes(ctx context.Context, wing string) (map[string]string, error)

	// Embedding operations
	GetEmbedding(ctx context.Context, id string) (*EmbeddingRow, error)
	GetAllEmbeddings(ctx context.Context, filter DrawerFilter) ([]EmbeddingRow, error)

	// Search operations
	KeywordSearch(ctx context.Context, query string, filter DrawerFilter, limit int) ([]SearchResult, error)

	// Hierarchy operations
	ListWings(ctx context.Context) ([]WingSummary, error)
	ListRooms(ctx context.Context, wing string) ([]RoomSummary, error)
	GetTaxonomy(ctx context.Context) (*Taxonomy, error)

	// Knowledge graph operations
	InsertEntity(ctx context.Context, e *Entity) error
	GetEntity(ctx context.Context, id string) (*Entity, error)
	InsertTriple(ctx context.Context, t *Triple) error
	QueryTriples(ctx context.Context, entityID, asOf, direction string) ([]*Triple, error)
	InvalidateTriple(ctx context.Context, subjectID, predicateID, objectID string) error
	TimelineTriples(ctx context.Context, entityID string) ([]*Triple, error)
	KGStats(ctx context.Context) (*KGStats, error)

	// Diary operations
	InsertDiaryEntry(ctx context.Context, d *DiaryEntry) error
	ListDiaryEntries(ctx context.Context, agent string, limit int) ([]*DiaryEntry, error)

	Close() error
}
