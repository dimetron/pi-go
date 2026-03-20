package memory

import "time"

// ObservationType classifies what an observation represents.
type ObservationType string

const (
	TypeDecision  ObservationType = "decision"
	TypeBugfix    ObservationType = "bugfix"
	TypeFeature   ObservationType = "feature"
	TypeRefactor  ObservationType = "refactor"
	TypeDiscovery ObservationType = "discovery"
	TypeChange    ObservationType = "change"
)

// ValidObservationTypes is the set of allowed observation types.
var ValidObservationTypes = map[ObservationType]bool{
	TypeDecision:  true,
	TypeBugfix:    true,
	TypeFeature:   true,
	TypeRefactor:  true,
	TypeDiscovery: true,
	TypeChange:    true,
}

// Session represents a single agent interaction session.
type Session struct {
	ID          int64
	SessionID   string
	Project     string
	UserPrompt  string
	StartedAt   time.Time
	CompletedAt *time.Time
	Status      string // "active", "completed", "failed"
}

// Observation is a compressed, structured record of a tool usage event.
type Observation struct {
	ID              int64
	SessionID       string
	Project         string
	Title           string
	Type            ObservationType
	Text            string
	SourceFiles     []string // stored as JSON array
	ToolName        string
	PromptNumber    int
	DiscoveryTokens int
	CreatedAt       time.Time
}

// SessionSummary is a structured summary generated at session end.
type SessionSummary struct {
	ID              int64
	SessionID       string
	Project         string
	Request         string
	Investigated    string
	Learned         string
	Completed       string
	NextSteps       string
	DiscoveryTokens int
	CreatedAt       time.Time
}

// RawObservation is the uncompressed tool event captured by AfterToolCallback.
type RawObservation struct {
	SessionID  string
	Project    string
	ToolName   string
	ToolInput  map[string]any
	ToolOutput map[string]any
	Timestamp  time.Time
}

// SearchQuery describes a full-text search request.
type SearchQuery struct {
	Query   string
	Project string
	Type    ObservationType
	Limit   int
	Offset  int
}

// SearchResultRow is a compact representation of one search match.
type SearchResultRow struct {
	ID        int64           `json:"id"`
	Title     string          `json:"title"`
	Type      ObservationType `json:"type"`
	CreatedAt time.Time       `json:"created_at"`
	ReadCost  int             `json:"read"`
	WorkCost  int             `json:"work"`
}

// SearchResult contains paginated search results.
type SearchResult struct {
	Rows  []SearchResultRow `json:"results"`
	Total int               `json:"total"`
}
