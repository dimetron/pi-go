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
	// ContentHash identifies the chunk's content. The miner compares it with the
	// stored hash to skip re-embedding chunks that have not changed; the ID alone
	// cannot tell, as it is derived from the source path and chunk index only.
	ContentHash string `json:"content_hash,omitempty"`
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

// Triple is a subject-predicate-object fact in the knowledge graph, in the
// shape the palace API has always returned. It is a view of one statement.
type Triple struct {
	// ID is "e<EID>".
	ID          string `json:"id"`
	EID         int64  `json:"eid"`
	SubjectID   string `json:"subject_id"`
	PredicateID string `json:"predicate_id"`
	ObjectID    string `json:"object_id"`
	// ObjectKind is empty for an entity object, else "string", "number", ...
	ObjectKind string     `json:"object_kind,omitempty"`
	ValidFrom  *time.Time `json:"valid_from,omitempty"`
	ValidTo    *time.Time `json:"valid_to,omitempty"`
	SourceFile string     `json:"source_file,omitempty"`
	// ExtractedAt is when the fact was learned: the legacy extraction time
	// for imported triples, otherwise the writing transaction's time.
	ExtractedAt   time.Time `json:"extracted_at"`
	TAdd          int64     `json:"t_add"`
	Confidence    *float64  `json:"confidence,omitempty"`
	Sources       []string  `json:"sources,omitempty"`
	Confirmations int       `json:"confirmations,omitempty"`
	// Status is "active", "ended", "superseded", "retracted", "cascade" or
	// "replaced"; SupersededBy names the correction of a superseded fact.
	Status       string `json:"status"`
	SupersededBy int64  `json:"superseded_by,omitempty"`
	// New is set by Add when the fact was written rather than already known.
	New bool `json:"new,omitempty"`
}

// TripleInput is the input for creating a new triple.
type TripleInput struct {
	Subject   string
	Predicate string
	Object    string
	ValidFrom *time.Time
	ValidTo   *time.Time
	// Literal stores Object as a string value instead of an entity.
	Literal bool
	// About annotates statement About instead of the Subject entity.
	About int64
	// Support derives the fact from these statements: it is retracted when
	// any of them is retracted or corrected.
	Support []int64
	// Confidence (0-1), Source and SourceFile become layers on the fact.
	Confidence *float64
	Source     string
	SourceFile string
	// ConfirmExisting confirms an identical fact that already exists.
	ConfirmExisting bool
	// Graph puts the fact into this named graph.
	Graph string
	// Author and Session are recorded on the transaction.
	Author  string
	Session string
}

func (in TripleInput) txOptions() TxOptions {
	return TxOptions{Author: in.Author, Source: in.Session}
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

// SearchResult is a single search match with similarity score and optional FTS5 rank.
type SearchResult struct {
	Drawer     Drawer  `json:"drawer"`
	Similarity float32 `json:"similarity"`
	Rank       int     `json:"rank,omitempty"` // FTS5 rank (negative = more relevant), 0 if semantic-only
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
	EntityCount int `json:"entity_count"`
	// TripleCount is the live facts; ActiveTriples those whose valid time
	// is still open.
	TripleCount   int `json:"triple_count"`
	ActiveTriples int `json:"active_triples"`
	// Statements counts every statement ever written, Layers the live
	// statements about statements, Retracted the retracted ones.
	Statements   int      `json:"statements"`
	Layers       int      `json:"layers"`
	Retracted    int      `json:"retracted"`
	Transactions int      `json:"transactions"`
	Predicates   []string `json:"predicates,omitempty"`
}

// ScoredResult is a drawer ID paired with a similarity score, used for ranking.
type ScoredResult struct {
	DrawerID   string  `json:"drawer_id"`
	Similarity float32 `json:"similarity"`
}

// GraphStats are statistics about the palace graph.
type GraphStats struct {
	TotalRooms  int      `json:"total_rooms"`
	TunnelCount int      `json:"tunnel_count"`
	EdgeCount   int      `json:"edge_count"`
	TopTunnels  []string `json:"top_tunnels,omitempty"`
}

// TraverseResult is a room discovered during BFS graph traversal.
type TraverseResult struct {
	Room        string   `json:"room"`
	Wings       []string `json:"wings"`
	DrawerCount int      `json:"drawer_count"`
	Hops        int      `json:"hops"`
}

// Tunnel is a room that appears in multiple wings, bridging them.
type Tunnel struct {
	Room        string   `json:"room"`
	Wings       []string `json:"wings"`
	DrawerCount int      `json:"drawer_count"`
}
