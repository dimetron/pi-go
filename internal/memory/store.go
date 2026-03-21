package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Store defines the interface for memory persistence operations.
type Store interface {
	CreateSession(ctx context.Context, s *Session) error
	CompleteSession(ctx context.Context, sessionID string) error
	InsertObservation(ctx context.Context, obs *Observation) error
	GetObservations(ctx context.Context, ids []int64) ([]*Observation, error)
	RecentObservations(ctx context.Context, project string, limit int) ([]*Observation, error)
	UpsertSummary(ctx context.Context, sum *SessionSummary) error
	RecentSummaries(ctx context.Context, project string, limit int) ([]*SessionSummary, error)
	Search(ctx context.Context, q SearchQuery) (*SearchResult, error)
	Timeline(ctx context.Context, anchorID int64, before, after int) ([]*Observation, error)
	Close() error
}

// SQLiteStore implements Store using a SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a new SQLiteStore wrapping the given database.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

// CreateSession inserts a new session record.
func (s *SQLiteStore) CreateSession(ctx context.Context, sess *Session) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (session_id, project, user_prompt, started_at, started_at_epoch, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		sess.SessionID,
		sess.Project,
		sess.UserPrompt,
		sess.StartedAt.UTC().Format(time.RFC3339),
		sess.StartedAt.Unix(),
		sess.Status,
	)
	if err != nil {
		return fmt.Errorf("memory: create session: %w", err)
	}
	return nil
}

// CompleteSession marks a session as completed with a timestamp.
func (s *SQLiteStore) CompleteSession(ctx context.Context, sessionID string) error {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`UPDATE sessions SET status = 'completed', completed_at = ?, completed_at_epoch = ? WHERE session_id = ?`,
		now.Format(time.RFC3339),
		now.Unix(),
		sessionID,
	)
	if err != nil {
		return fmt.Errorf("memory: complete session: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("memory: session %q not found", sessionID)
	}
	return nil
}

// InsertObservation inserts a new observation, marshaling SourceFiles as JSON.
func (s *SQLiteStore) InsertObservation(ctx context.Context, obs *Observation) error {
	sourceFiles, err := json.Marshal(obs.SourceFiles)
	if err != nil {
		return fmt.Errorf("memory: marshal source_files: %w", err)
	}

	result, err := s.db.ExecContext(ctx,
		`INSERT INTO observations (session_id, project, title, type, text, source_files, tool_name, prompt_number, discovery_tokens, created_at, created_at_epoch)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		obs.SessionID,
		obs.Project,
		obs.Title,
		string(obs.Type),
		obs.Text,
		string(sourceFiles),
		obs.ToolName,
		obs.PromptNumber,
		obs.DiscoveryTokens,
		obs.CreatedAt.UTC().Format(time.RFC3339),
		obs.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("memory: insert observation: %w", err)
	}

	id, err := result.LastInsertId()
	if err == nil {
		obs.ID = id
	}
	return nil
}

// GetObservations fetches observations by IDs. Missing IDs are silently skipped.
func (s *SQLiteStore) GetObservations(ctx context.Context, ids []int64) ([]*Observation, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	// Build query with placeholders
	query := "SELECT id, session_id, project, title, type, text, source_files, tool_name, prompt_number, discovery_tokens, created_at FROM observations WHERE id IN ("
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			query += ","
		}
		query += "?"
		args[i] = id
	}
	query += ") ORDER BY created_at_epoch ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: get observations: %w", err)
	}
	defer rows.Close()

	return scanObservations(rows)
}

// RecentObservations returns the most recent observations for a project.
func (s *SQLiteStore) RecentObservations(ctx context.Context, project string, limit int) ([]*Observation, error) {
	if limit <= 0 {
		limit = 50
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, project, title, type, text, source_files, tool_name, prompt_number, discovery_tokens, created_at
		 FROM observations WHERE project = ? ORDER BY created_at_epoch DESC LIMIT ?`,
		project, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("memory: recent observations: %w", err)
	}
	defer rows.Close()

	return scanObservations(rows)
}

// UpsertSummary inserts or replaces a session summary.
func (s *SQLiteStore) UpsertSummary(ctx context.Context, sum *SessionSummary) error {
	result, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO session_summaries (session_id, project, request, investigated, learned, completed, next_steps, discovery_tokens, created_at, created_at_epoch)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sum.SessionID,
		sum.Project,
		sum.Request,
		sum.Investigated,
		sum.Learned,
		sum.Completed,
		sum.NextSteps,
		sum.DiscoveryTokens,
		sum.CreatedAt.UTC().Format(time.RFC3339),
		sum.CreatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("memory: upsert summary: %w", err)
	}

	id, err := result.LastInsertId()
	if err == nil {
		sum.ID = id
	}
	return nil
}

// RecentSummaries returns the most recent session summaries for a project.
func (s *SQLiteStore) RecentSummaries(ctx context.Context, project string, limit int) ([]*SessionSummary, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, project, request, investigated, learned, completed, next_steps, discovery_tokens, created_at
		 FROM session_summaries WHERE project = ? ORDER BY created_at_epoch DESC LIMIT ?`,
		project, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("memory: recent summaries: %w", err)
	}
	defer rows.Close()

	var summaries []*SessionSummary
	for rows.Next() {
		sum := &SessionSummary{}
		var createdAt string
		if err := rows.Scan(
			&sum.ID, &sum.SessionID, &sum.Project,
			&sum.Request, &sum.Investigated, &sum.Learned, &sum.Completed, &sum.NextSteps,
			&sum.DiscoveryTokens, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("memory: scan summary: %w", err)
		}
		sum.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		summaries = append(summaries, sum)
	}
	return summaries, rows.Err()
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// scanObservations reads observation rows from a query result.
func scanObservations(rows *sql.Rows) ([]*Observation, error) {
	var observations []*Observation
	for rows.Next() {
		obs := &Observation{}
		var sourceFilesJSON, createdAt string
		if err := rows.Scan(
			&obs.ID, &obs.SessionID, &obs.Project,
			&obs.Title, &obs.Type, &obs.Text,
			&sourceFilesJSON, &obs.ToolName,
			&obs.PromptNumber, &obs.DiscoveryTokens,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("memory: scan observation: %w", err)
		}
		obs.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		if sourceFilesJSON != "" {
			_ = json.Unmarshal([]byte(sourceFilesJSON), &obs.SourceFiles)
		}
		if obs.SourceFiles == nil {
			obs.SourceFiles = []string{}
		}
		observations = append(observations, obs)
	}
	return observations, rows.Err()
}
