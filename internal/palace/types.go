package palace

import "time"

// Drawer is the fundamental unit of knowledge in the palace.
type Drawer struct {
	ID         string    `json:"id"`
	Wing       string    `json:"wing"`
	Room       string    `json:"room"`
	Hall       string    `json:"hall,omitempty"`
	Content    string    `json:"content"`
	SourceFile string    `json:"source_file,omitempty"`
	ChunkIndex int       `json:"chunk_index,omitempty"`
	AddedBy    string    `json:"added_by,omitempty"`
	Importance int       `json:"importance"` // 0-10
	Embedding  []float32 `json:"-"`          // not serialized
	CreatedAt  time.Time `json:"created_at"`
}

// DrawerInput is the input for creating a new drawer.
type DrawerInput struct {
	Wing       string
	Room       string
	Hall       string
	Content    string
	SourceFile string
	ChunkIndex int
	AddedBy    string
	Importance int
}

// DrawerFilter constrains drawer queries.
type DrawerFilter struct {
	Wing string
	Room string
	Hall string
}

// Entity is a node in the knowledge graph.
type Entity struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type,omitempty"` // person, file, concept, etc.
	CreatedAt time.Time `json:"created_at"`
}

// Triple is a subject-predicate-object fact in the knowledge graph.
type Triple struct {
	ID          string     `json:"id"`
	SubjectID   string     `json:"subject_id"`
	PredicateID string     `json:"predicate_id"`
	ObjectID    string     `json:"object_id"`
	ValidFrom   *time.Time `json:"valid_from,omitempty"`
	ValidTo     *time.Time `json:"valid_to,omitempty"`
	SourceFile  string     `json:"source_file,omitempty"`
	ExtractedAt time.Time  `json:"extracted_at"`
}

// TripleInput is the input for creating a new triple.
type TripleInput struct {
	Subject   string
	Predicate string
	Object    string
	ValidFrom *time.Time
}

// DiaryEntry is a journal entry written by an agent.
type DiaryEntry struct {
	ID        int64     `json:"id"`
	Agent     string    `json:"agent"`
	Entry     string    `json:"entry"`
	Topic     string    `json:"topic,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// WingSummary is an aggregate view of a wing.
type WingSummary struct {
	Wing        string `json:"wing"`
	DrawerCount int    `json:"drawer_count"`
	RoomCount   int    `json:"room_count"`
}

// RoomSummary is an aggregate view of a room.
type RoomSummary struct {
	Room        string   `json:"room"`
	Wing        string   `json:"wing"`
	DrawerCount int      `json:"drawer_count"`
	Halls       []string `json:"halls,omitempty"`
}

// Taxonomy is the full wing → room hierarchy.
type Taxonomy struct {
	Wings []TaxonomyWing `json:"wings"`
}

// TaxonomyWing is a wing entry in the taxonomy.
type TaxonomyWing struct {
	Name  string         `json:"name"`
	Rooms []TaxonomyRoom `json:"rooms"`
}

// TaxonomyRoom is a room entry in the taxonomy.
type TaxonomyRoom struct {
	Name        string `json:"name"`
	DrawerCount int    `json:"drawer_count"`
}

// SearchQuery describes a palace search request.
type SearchQuery struct {
	Query string
	Wing  string
	Room  string
	Limit int
}

// SearchResult is a single search match with similarity score.
type SearchResult struct {
	Drawer     Drawer  `json:"drawer"`
	Similarity float32 `json:"similarity"`
}

// DuplicateResult describes a potential duplicate drawer.
type DuplicateResult struct {
	ExistingID string  `json:"existing_id"`
	Similarity float32 `json:"similarity"`
}

// EmbeddingRow is a drawer ID paired with its embedding vector.
type EmbeddingRow struct {
	DrawerID  string
	Embedding []float32
}

// PalaceStatus is an aggregate status of the entire palace.
type PalaceStatus struct {
	DrawerCount int      `json:"drawer_count"`
	WingCount   int      `json:"wing_count"`
	RoomCount   int      `json:"room_count"`
	KG          *KGStats `json:"kg,omitempty"`
	ModelLoaded bool     `json:"model_loaded"`
}

// KGStats are aggregate statistics for the knowledge graph.
type KGStats struct {
	EntityCount   int      `json:"entity_count"`
	TripleCount   int      `json:"triple_count"`
	ActiveTriples int      `json:"active_triples"`
	Predicates    []string `json:"predicates,omitempty"`
}

// GraphStats are statistics about the palace graph.
type GraphStats struct {
	TotalRooms  int      `json:"total_rooms"`
	TunnelCount int      `json:"tunnel_count"`
	EdgeCount   int      `json:"edge_count"`
	TopTunnels  []string `json:"top_tunnels,omitempty"`
}
