package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// estimateTokens gives a rough token count for a string (1 token ~ 4 chars).
func estimateTokens(s string) int {
	return len(s) / 4
}

// Search performs FTS5 full-text search over observations, returning compact result rows.
// Falls back to LIKE matching if FTS5 is unavailable.
func (s *SQLiteStore) Search(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	if q.Query == "" {
		return &SearchResult{}, nil
	}
	if q.Limit <= 0 {
		q.Limit = 20
	}
	if q.Offset < 0 {
		q.Offset = 0
	}

	if HasFTS5(s.db) {
		return s.searchFTS5(ctx, q)
	}
	return s.searchLike(ctx, q)
}

// searchFTS5 uses the observations_fts virtual table for ranked search.
func (s *SQLiteStore) searchFTS5(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	// Build WHERE clauses for the main observations table join
	var filters []string
	var args []any

	// FTS5 match
	ftsQuery := sanitizeFTS5Query(q.Query)
	filters = append(filters, "f.observations_fts MATCH ?")
	args = append(args, ftsQuery)

	if q.Project != "" {
		filters = append(filters, "o.project = ?")
		args = append(args, q.Project)
	}
	if q.Type != "" {
		filters = append(filters, "o.type = ?")
		args = append(args, string(q.Type))
	}

	where := strings.Join(filters, " AND ")

	// Count total matches
	countQuery := fmt.Sprintf(
		`SELECT COUNT(*) FROM observations_fts f JOIN observations o ON o.id = f.rowid WHERE %s`, where)
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("memory: search count: %w", err)
	}

	// Fetch page of results, ranked by FTS5 relevance
	selectQuery := fmt.Sprintf(
		`SELECT o.id, o.title, o.type, o.text, o.discovery_tokens, o.created_at
		 FROM observations_fts f
		 JOIN observations o ON o.id = f.rowid
		 WHERE %s
		 ORDER BY rank
		 LIMIT ? OFFSET ?`, where)
	args = append(args, q.Limit, q.Offset)

	rows, err := s.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: search query: %w", err)
	}
	defer rows.Close()

	return scanSearchRows(rows, total)
}

// searchLike is a fallback when FTS5 is unavailable, using LIKE on title and text.
func (s *SQLiteStore) searchLike(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	pattern := "%" + q.Query + "%"

	var filters []string
	var args []any

	filters = append(filters, "(title LIKE ? OR text LIKE ?)")
	args = append(args, pattern, pattern)

	if q.Project != "" {
		filters = append(filters, "project = ?")
		args = append(args, q.Project)
	}
	if q.Type != "" {
		filters = append(filters, "type = ?")
		args = append(args, string(q.Type))
	}

	where := strings.Join(filters, " AND ")

	// Count total
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM observations WHERE %s", where)
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, fmt.Errorf("memory: search count: %w", err)
	}

	// Fetch page
	selectQuery := fmt.Sprintf(
		`SELECT id, title, type, text, discovery_tokens, created_at
		 FROM observations WHERE %s
		 ORDER BY created_at_epoch DESC
		 LIMIT ? OFFSET ?`, where)
	args = append(args, q.Limit, q.Offset)

	rows, err := s.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: search query: %w", err)
	}
	defer rows.Close()

	return scanSearchRows(rows, total)
}

// scanSearchRows reads search result rows into a SearchResult.
func scanSearchRows(rows *sql.Rows, total int) (*SearchResult, error) {
	var result SearchResult
	result.Total = total

	for rows.Next() {
		var row SearchResultRow
		var text, createdAt string
		var discoveryTokens int
		if err := rows.Scan(&row.ID, &row.Title, &row.Type, &text, &discoveryTokens, &createdAt); err != nil {
			return nil, fmt.Errorf("memory: scan search row: %w", err)
		}
		parseTime, _ := parseRFC3339(createdAt)
		row.CreatedAt = parseTime
		row.ReadCost = estimateTokens(text)
		row.WorkCost = discoveryTokens
		result.Rows = append(result.Rows, row)
	}
	if result.Rows == nil {
		result.Rows = []SearchResultRow{}
	}
	return &result, rows.Err()
}

// Timeline returns observations around an anchor observation, from the same project.
// It fetches `before` observations created before the anchor and `after` observations after it.
func (s *SQLiteStore) Timeline(ctx context.Context, anchorID int64, before, after int) ([]*Observation, error) {
	if before <= 0 {
		before = 5
	}
	if after <= 0 {
		after = 5
	}

	// Get the anchor observation's project and epoch
	var project string
	var epoch int64
	err := s.db.QueryRowContext(ctx,
		"SELECT project, created_at_epoch FROM observations WHERE id = ?", anchorID,
	).Scan(&project, &epoch)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("memory: anchor observation %d not found", anchorID)
		}
		return nil, fmt.Errorf("memory: get anchor: %w", err)
	}

	// Fetch `before` observations created before the anchor (DESC, then reverse)
	beforeRows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, project, title, type, text, source_files, tool_name, prompt_number, discovery_tokens, created_at
		 FROM observations
		 WHERE project = ? AND created_at_epoch < ? AND id != ?
		 ORDER BY created_at_epoch DESC
		 LIMIT ?`,
		project, epoch, anchorID, before,
	)
	if err != nil {
		return nil, fmt.Errorf("memory: timeline before: %w", err)
	}
	defer beforeRows.Close()
	beforeObs, err := scanObservations(beforeRows)
	if err != nil {
		return nil, err
	}

	// Reverse beforeObs to get chronological order
	for i, j := 0, len(beforeObs)-1; i < j; i, j = i+1, j-1 {
		beforeObs[i], beforeObs[j] = beforeObs[j], beforeObs[i]
	}

	// Fetch the anchor itself
	anchorObs, err := s.GetObservations(ctx, []int64{anchorID})
	if err != nil {
		return nil, err
	}

	// Fetch `after` observations created after the anchor (ASC)
	afterRows, err := s.db.QueryContext(ctx,
		`SELECT id, session_id, project, title, type, text, source_files, tool_name, prompt_number, discovery_tokens, created_at
		 FROM observations
		 WHERE project = ? AND created_at_epoch > ? AND id != ?
		 ORDER BY created_at_epoch ASC
		 LIMIT ?`,
		project, epoch, anchorID, after,
	)
	if err != nil {
		return nil, fmt.Errorf("memory: timeline after: %w", err)
	}
	defer afterRows.Close()
	afterObs, err := scanObservations(afterRows)
	if err != nil {
		return nil, err
	}

	// Combine: before + anchor + after
	result := make([]*Observation, 0, len(beforeObs)+len(anchorObs)+len(afterObs))
	result = append(result, beforeObs...)
	result = append(result, anchorObs...)
	result = append(result, afterObs...)
	return result, nil
}

// SearchSummaries performs FTS5 search over session summaries.
// Falls back to LIKE if FTS5 is unavailable.
func (s *SQLiteStore) SearchSummaries(ctx context.Context, query string, project string) ([]*SessionSummary, error) {
	if query == "" {
		return nil, nil
	}

	if HasFTS5(s.db) {
		return s.searchSummariesFTS5(ctx, query, project)
	}
	return s.searchSummariesLike(ctx, query, project)
}

func (s *SQLiteStore) searchSummariesFTS5(ctx context.Context, query string, project string) ([]*SessionSummary, error) {
	ftsQuery := sanitizeFTS5Query(query)

	var sqlStr string
	var args []any

	if project != "" {
		sqlStr = `SELECT ss.id, ss.session_id, ss.project, ss.request, ss.investigated, ss.learned, ss.completed, ss.next_steps, ss.discovery_tokens, ss.created_at
			FROM session_summaries_fts f
			JOIN session_summaries ss ON ss.id = f.rowid
			WHERE f.session_summaries_fts MATCH ? AND ss.project = ?
			ORDER BY rank`
		args = []any{ftsQuery, project}
	} else {
		sqlStr = `SELECT ss.id, ss.session_id, ss.project, ss.request, ss.investigated, ss.learned, ss.completed, ss.next_steps, ss.discovery_tokens, ss.created_at
			FROM session_summaries_fts f
			JOIN session_summaries ss ON ss.id = f.rowid
			WHERE f.session_summaries_fts MATCH ?
			ORDER BY rank`
		args = []any{ftsQuery}
	}

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: search summaries: %w", err)
	}
	defer rows.Close()

	return scanSummaryRows(rows)
}

func (s *SQLiteStore) searchSummariesLike(ctx context.Context, query string, project string) ([]*SessionSummary, error) {
	pattern := "%" + query + "%"

	var sqlStr string
	var args []any

	if project != "" {
		sqlStr = `SELECT id, session_id, project, request, investigated, learned, completed, next_steps, discovery_tokens, created_at
			FROM session_summaries
			WHERE (request LIKE ? OR investigated LIKE ? OR learned LIKE ? OR completed LIKE ? OR next_steps LIKE ?)
			AND project = ?
			ORDER BY created_at_epoch DESC`
		args = []any{pattern, pattern, pattern, pattern, pattern, project}
	} else {
		sqlStr = `SELECT id, session_id, project, request, investigated, learned, completed, next_steps, discovery_tokens, created_at
			FROM session_summaries
			WHERE (request LIKE ? OR investigated LIKE ? OR learned LIKE ? OR completed LIKE ? OR next_steps LIKE ?)
			ORDER BY created_at_epoch DESC`
		args = []any{pattern, pattern, pattern, pattern, pattern}
	}

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("memory: search summaries: %w", err)
	}
	defer rows.Close()

	return scanSummaryRows(rows)
}

// scanSummaryRows reads session summary rows from a query result.
func scanSummaryRows(rows *sql.Rows) ([]*SessionSummary, error) {
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
		sum.CreatedAt, _ = parseRFC3339(createdAt)
		summaries = append(summaries, sum)
	}
	return summaries, rows.Err()
}

// parseRFC3339 parses a time string, returning zero time on failure.
func parseRFC3339(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// sanitizeFTS5Query escapes special FTS5 characters to prevent query syntax errors.
// It wraps each term in double quotes to treat them as literals.
func sanitizeFTS5Query(query string) string {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return query
	}
	// Quote each term to escape special characters
	quoted := make([]string, len(terms))
	for i, term := range terms {
		// Remove any existing double quotes to prevent injection
		clean := strings.ReplaceAll(term, "\"", "")
		if clean == "" {
			continue
		}
		quoted[i] = "\"" + clean + "\""
	}
	return strings.Join(quoted, " ")
}
