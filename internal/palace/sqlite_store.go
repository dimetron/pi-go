package palace

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SQLitePalaceStore implements PalaceStore using a SQLite database.
type SQLitePalaceStore struct {
	db *sql.DB
}

// NewSQLitePalaceStore creates a new store wrapping the given database.
func NewSQLitePalaceStore(db *sql.DB) *SQLitePalaceStore {
	return &SQLitePalaceStore{db: db}
}

// GenerateDrawerID creates a deterministic drawer ID from wing, room, and content identity.
func GenerateDrawerID(wing, room, sourceFile string, chunkIndex int, content string) string {
	var key string
	if sourceFile != "" {
		key = fmt.Sprintf("%s:%d", sourceFile, chunkIndex)
	} else {
		// Use content prefix + timestamp for ad-hoc drawers
		prefix := content
		if len(prefix) > 100 {
			prefix = prefix[:100]
		}
		key = prefix
	}
	h := md5.Sum([]byte(key))
	return fmt.Sprintf("drawer_%s_%s_%x", wing, room, h[:8])
}

// HashContent returns the content hash stored on a drawer and compared by the
// miner to decide whether a chunk needs re-embedding.
func HashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}

// DrawerHashes returns id → content_hash for every drawer in a wing.
//
// This is what makes mining incremental: the miner looks up each chunk's ID,
// compares the stored hash with the hash of the chunk it just read, and only
// embeds the ones that differ. Rows written before the content_hash migration
// carry ” and so never match, which forces exactly one re-embed and records
// their hash.
func (s *SQLitePalaceStore) DrawerHashes(ctx context.Context, wing string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, content_hash FROM drawers WHERE wing = ?`, wing)
	if err != nil {
		return nil, fmt.Errorf("palace: drawer hashes: %w", err)
	}
	defer rows.Close()

	hashes := make(map[string]string)
	for rows.Next() {
		var id, hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return nil, fmt.Errorf("palace: scan drawer hash: %w", err)
		}
		hashes[id] = hash
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("palace: drawer hashes: %w", err)
	}
	return hashes, nil
}

// InsertDrawer inserts a new drawer record.
func (s *SQLitePalaceStore) InsertDrawer(ctx context.Context, d *Drawer) error {
	var embeddingBlob []byte
	if len(d.Embedding) > 0 {
		embeddingBlob = MarshalEmbedding(d.Embedding)
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO drawers (id, wing, room, hall, content, source_file, chunk_index, added_by, importance, embedding, created_at, created_at_epoch, content_hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID,
		d.Wing,
		d.Room,
		d.Hall,
		d.Content,
		d.SourceFile,
		d.ChunkIndex,
		d.AddedBy,
		d.Importance,
		embeddingBlob,
		d.CreatedAt.UTC().Format(time.RFC3339),
		d.CreatedAt.Unix(),
		d.ContentHash,
	)
	if err != nil {
		return fmt.Errorf("palace: insert drawer: %w", err)
	}
	return nil
}

// BatchInsertDrawers inserts multiple drawers in a single transaction.
// Returns the number of drawers inserted.
func (s *SQLitePalaceStore) BatchInsertDrawers(ctx context.Context, drawers []*Drawer) (int, error) {
	if len(drawers) == 0 {
		return 0, nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("palace: begin batch transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR REPLACE INTO drawers (id, wing, room, hall, content, source_file, chunk_index, added_by, importance, embedding, created_at, created_at_epoch, content_hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return 0, fmt.Errorf("palace: prepare batch statement: %w", err)
	}
	defer stmt.Close()

	count := 0
	for _, d := range drawers {
		var embeddingBlob []byte
		if len(d.Embedding) > 0 {
			embeddingBlob = MarshalEmbedding(d.Embedding)
		}

		_, err := stmt.ExecContext(ctx,
			d.ID,
			d.Wing,
			d.Room,
			d.Hall,
			d.Content,
			d.SourceFile,
			d.ChunkIndex,
			d.AddedBy,
			d.Importance,
			embeddingBlob,
			d.CreatedAt.UTC().Format(time.RFC3339),
			d.CreatedAt.Unix(),
			d.ContentHash,
		)
		if err != nil {
			return count, fmt.Errorf("palace: batch insert drawer: %w", err)
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return count, fmt.Errorf("palace: commit batch: %w", err)
	}

	return count, nil
}

// DeleteDrawer removes a drawer by ID.
func (s *SQLitePalaceStore) DeleteDrawer(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM drawers WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("palace: delete drawer: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("palace: drawer %q not found", id)
	}
	return nil
}

// GetDrawer fetches a single drawer by ID.
func (s *SQLitePalaceStore) GetDrawer(ctx context.Context, id string) (*Drawer, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, wing, room, hall, content, source_file, chunk_index, added_by, importance, embedding, created_at
		 FROM drawers WHERE id = ?`, id)

	d, err := scanDrawer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("palace: drawer %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("palace: get drawer: %w", err)
	}
	return d, nil
}

// ListDrawers returns drawers matching the filter, ordered by importance desc then created_at desc.
func (s *SQLitePalaceStore) ListDrawers(ctx context.Context, filter DrawerFilter) ([]*Drawer, error) {
	query := "SELECT id, wing, room, hall, content, source_file, chunk_index, added_by, importance, embedding, created_at FROM drawers"
	var conditions []string
	var args []any

	if filter.Wing != "" {
		conditions = append(conditions, "wing = ?")
		args = append(args, filter.Wing)
	}
	if filter.Room != "" {
		conditions = append(conditions, "room = ?")
		args = append(args, filter.Room)
	}
	if filter.Hall != "" {
		conditions = append(conditions, "hall = ?")
		args = append(args, filter.Hall)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY importance DESC, created_at_epoch DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("palace: list drawers: %w", err)
	}
	defer rows.Close()

	return scanDrawers(rows)
}

// CountDrawers returns the total number of drawers.
func (s *SQLitePalaceStore) CountDrawers(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM drawers").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("palace: count drawers: %w", err)
	}
	return count, nil
}

// GetEmbedding returns the embedding for a single drawer.
func (s *SQLitePalaceStore) GetEmbedding(ctx context.Context, id string) (*EmbeddingRow, error) {
	var blob []byte
	err := s.db.QueryRowContext(ctx, "SELECT embedding FROM drawers WHERE id = ?", id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("palace: drawer %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("palace: get embedding: %w", err)
	}
	return &EmbeddingRow{
		DrawerID:  id,
		Embedding: UnmarshalEmbedding(blob),
	}, nil
}

// GetAllEmbeddings returns all embeddings matching the filter.
func (s *SQLitePalaceStore) GetAllEmbeddings(ctx context.Context, filter DrawerFilter) ([]EmbeddingRow, error) {
	query := "SELECT id, embedding FROM drawers WHERE embedding IS NOT NULL"
	var args []any

	if filter.Wing != "" {
		query += " AND wing = ?"
		args = append(args, filter.Wing)
	}
	if filter.Room != "" {
		query += " AND room = ?"
		args = append(args, filter.Room)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("palace: get all embeddings: %w", err)
	}
	defer rows.Close()

	var results []EmbeddingRow
	for rows.Next() {
		var id string
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, fmt.Errorf("palace: scan embedding: %w", err)
		}
		results = append(results, EmbeddingRow{
			DrawerID:  id,
			Embedding: UnmarshalEmbedding(blob),
		})
	}
	return results, rows.Err()
}

// KeywordSearch performs an FTS5 keyword search on the drawers_fts table,
// falling back to LIKE if FTS5 is unavailable. Results are ordered by FTS5 rank.
func (s *SQLitePalaceStore) KeywordSearch(ctx context.Context, query string, filter DrawerFilter, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}

	ftsQuery := sanitizeFTS5Query(query)
	if ftsQuery == "" {
		return nil, nil
	}

	q := `SELECT d.id, d.wing, d.room, d.hall, d.content, d.source_file,
	             d.chunk_index, d.added_by, d.importance, d.embedding, d.created_at,
	             rank
	      FROM drawers_fts f
	      JOIN drawers d ON d.rowid = f.rowid
	      WHERE f.drawers_fts MATCH ?`

	var args []any
	args = append(args, ftsQuery)

	if filter.Wing != "" {
		q += " AND d.wing = ?"
		args = append(args, filter.Wing)
	}
	if filter.Room != "" {
		q += " AND d.room = ?"
		args = append(args, filter.Room)
	}

	q += " ORDER BY rank LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		// Fall back to LIKE search if FTS5 fails
		return s.likeSearch(ctx, query, filter, limit)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var d Drawer
		var blob []byte
		var createdAt string
		var rank float64
		if err := rows.Scan(
			&d.ID, &d.Wing, &d.Room, &d.Hall,
			&d.Content, &d.SourceFile, &d.ChunkIndex,
			&d.AddedBy, &d.Importance, &blob, &createdAt,
			&rank,
		); err != nil {
			return nil, fmt.Errorf("palace: scan keyword search: %w", err)
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		d.Embedding = UnmarshalEmbedding(blob)
		results = append(results, SearchResult{
			Drawer:     d,
			Similarity: 0, Rank: int(rank),
		})
	}
	return results, rows.Err()
}

// likeSearch is a fallback when FTS5 is unavailable.
func (s *SQLitePalaceStore) likeSearch(ctx context.Context, query string, filter DrawerFilter, limit int) ([]SearchResult, error) {
	q := `SELECT id, wing, room, hall, content, source_file,
	             chunk_index, added_by, importance, embedding, created_at
	      FROM drawers WHERE content LIKE ?`
	var args []any
	args = append(args, "%"+query+"%")

	if filter.Wing != "" {
		q += " AND wing = ?"
		args = append(args, filter.Wing)
	}
	if filter.Room != "" {
		q += " AND room = ?"
		args = append(args, filter.Room)
	}

	q += " ORDER BY importance DESC, created_at_epoch DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("palace: like search: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var d Drawer
		var blob []byte
		var createdAt string
		if err := rows.Scan(
			&d.ID, &d.Wing, &d.Room, &d.Hall,
			&d.Content, &d.SourceFile, &d.ChunkIndex,
			&d.AddedBy, &d.Importance, &blob, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("palace: scan like search: %w", err)
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		d.Embedding = UnmarshalEmbedding(blob)
		results = append(results, SearchResult{Drawer: d, Rank: -1})
	}
	return results, rows.Err()
}

// sanitizeFTS5Query escapes special FTS5 characters by quoting each term.
func sanitizeFTS5Query(query string) string {
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		clean := strings.ReplaceAll(term, "\"", "")
		if clean == "" {
			continue
		}
		quoted = append(quoted, "\""+clean+"\"")
	}
	return strings.Join(quoted, " ")
}

// ListWings returns aggregate wing summaries.
func (s *SQLitePalaceStore) ListWings(ctx context.Context) ([]WingSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT wing, COUNT(*), COUNT(DISTINCT room) FROM drawers GROUP BY wing ORDER BY wing")
	if err != nil {
		return nil, fmt.Errorf("palace: list wings: %w", err)
	}
	defer rows.Close()

	var wings []WingSummary
	for rows.Next() {
		var w WingSummary
		if err := rows.Scan(&w.Wing, &w.DrawerCount, &w.RoomCount); err != nil {
			return nil, fmt.Errorf("palace: scan wing: %w", err)
		}
		wings = append(wings, w)
	}
	return wings, rows.Err()
}

// ListRooms returns rooms within a wing with their halls.
func (s *SQLitePalaceStore) ListRooms(ctx context.Context, wing string) ([]RoomSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT room, wing, COUNT(*) FROM drawers WHERE wing = ? GROUP BY room ORDER BY room",
		wing)
	if err != nil {
		return nil, fmt.Errorf("palace: list rooms: %w", err)
	}
	defer rows.Close()

	var rooms []RoomSummary
	for rows.Next() {
		var r RoomSummary
		if err := rows.Scan(&r.Room, &r.Wing, &r.DrawerCount); err != nil {
			return nil, fmt.Errorf("palace: scan room: %w", err)
		}
		rooms = append(rooms, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Fetch halls for each room
	for i := range rooms {
		hallRows, err := s.db.QueryContext(ctx,
			"SELECT DISTINCT hall FROM drawers WHERE wing = ? AND room = ? AND hall != '' ORDER BY hall",
			wing, rooms[i].Room)
		if err != nil {
			return nil, fmt.Errorf("palace: list halls: %w", err)
		}
		for hallRows.Next() {
			var hall string
			if err := hallRows.Scan(&hall); err != nil {
				hallRows.Close()
				return nil, fmt.Errorf("palace: scan hall: %w", err)
			}
			rooms[i].Halls = append(rooms[i].Halls, hall)
		}
		hallRows.Close()
	}

	return rooms, nil
}

// GetTaxonomy returns the complete wing → room hierarchy.
func (s *SQLitePalaceStore) GetTaxonomy(ctx context.Context) (*Taxonomy, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT wing, room, COUNT(*) FROM drawers GROUP BY wing, room ORDER BY wing, room")
	if err != nil {
		return nil, fmt.Errorf("palace: get taxonomy: %w", err)
	}
	defer rows.Close()

	wingMap := make(map[string]*TaxonomyWing)
	var wingOrder []string

	for rows.Next() {
		var wing, room string
		var count int
		if err := rows.Scan(&wing, &room, &count); err != nil {
			return nil, fmt.Errorf("palace: scan taxonomy: %w", err)
		}
		tw, ok := wingMap[wing]
		if !ok {
			tw = &TaxonomyWing{Name: wing}
			wingMap[wing] = tw
			wingOrder = append(wingOrder, wing)
		}
		tw.Rooms = append(tw.Rooms, TaxonomyRoom{Name: room, DrawerCount: count})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	tax := &Taxonomy{}
	for _, name := range wingOrder {
		tax.Wings = append(tax.Wings, *wingMap[name])
	}
	return tax, nil
}

// InsertEntity inserts a new entity record.
func (s *SQLitePalaceStore) InsertEntity(ctx context.Context, e *Entity) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO entities (id, name, type, created_at) VALUES (?, ?, ?, ?)",
		e.ID, e.Name, e.Type, e.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("palace: insert entity: %w", err)
	}
	return nil
}

// GetEntity fetches an entity by ID.
func (s *SQLitePalaceStore) GetEntity(ctx context.Context, id string) (*Entity, error) {
	var e Entity
	var createdAt string
	err := s.db.QueryRowContext(ctx,
		"SELECT id, name, type, created_at FROM entities WHERE id = ?", id).
		Scan(&e.ID, &e.Name, &e.Type, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("palace: entity %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("palace: get entity: %w", err)
	}
	e.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &e, nil
}

// InsertTriple inserts a new triple record.
func (s *SQLitePalaceStore) InsertTriple(ctx context.Context, t *Triple) error {
	var validFrom, validTo sql.NullString
	if t.ValidFrom != nil {
		validFrom = sql.NullString{String: t.ValidFrom.UTC().Format(time.RFC3339), Valid: true}
	}
	if t.ValidTo != nil {
		validTo = sql.NullString{String: t.ValidTo.UTC().Format(time.RFC3339), Valid: true}
	}

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO triples (id, subject_id, predicate_id, object_id, valid_from, valid_to, source_file, extracted_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.SubjectID, t.PredicateID, t.ObjectID,
		validFrom, validTo, t.SourceFile,
		t.ExtractedAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("palace: insert triple: %w", err)
	}
	return nil
}

// QueryTriples returns triples involving the entity, optionally filtered by time.
func (s *SQLitePalaceStore) QueryTriples(ctx context.Context, entityID, asOf, direction string) ([]*Triple, error) {
	query := "SELECT id, subject_id, predicate_id, object_id, valid_from, valid_to, source_file, extracted_at FROM triples WHERE "
	var args []any

	switch direction {
	case "subject":
		query += "subject_id = ?"
		args = append(args, entityID)
	case "object":
		query += "object_id = ?"
		args = append(args, entityID)
	default: // "both" or empty
		query += "(subject_id = ? OR object_id = ?)"
		args = append(args, entityID, entityID)
	}

	if asOf != "" {
		query += " AND (valid_from IS NULL OR valid_from <= ?) AND (valid_to IS NULL OR valid_to >= ?)"
		args = append(args, asOf, asOf)
	}

	query += " ORDER BY extracted_at DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("palace: query triples: %w", err)
	}
	defer rows.Close()

	return scanTriples(rows)
}

// InvalidateTriple sets valid_to = now() on matching active triples.
func (s *SQLitePalaceStore) InvalidateTriple(ctx context.Context, subjectID, predicateID, objectID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		"UPDATE triples SET valid_to = ? WHERE subject_id = ? AND predicate_id = ? AND object_id = ? AND valid_to IS NULL",
		now, subjectID, predicateID, objectID)
	if err != nil {
		return fmt.Errorf("palace: invalidate triple: %w", err)
	}
	return nil
}

// TimelineTriples returns all triples for an entity ordered by extracted_at.
func (s *SQLitePalaceStore) TimelineTriples(ctx context.Context, entityID string) ([]*Triple, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, subject_id, predicate_id, object_id, valid_from, valid_to, source_file, extracted_at
		 FROM triples WHERE subject_id = ? OR object_id = ? ORDER BY extracted_at ASC`,
		entityID, entityID)
	if err != nil {
		return nil, fmt.Errorf("palace: timeline triples: %w", err)
	}
	defer rows.Close()

	return scanTriples(rows)
}

// KGStats returns aggregate statistics about the knowledge graph.
func (s *SQLitePalaceStore) KGStats(ctx context.Context) (*KGStats, error) {
	stats := &KGStats{}

	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM entities").Scan(&stats.EntityCount); err != nil {
		return nil, fmt.Errorf("palace: kg stats entities: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM triples").Scan(&stats.TripleCount); err != nil {
		return nil, fmt.Errorf("palace: kg stats triples: %w", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM triples WHERE valid_to IS NULL").Scan(&stats.ActiveTriples); err != nil {
		return nil, fmt.Errorf("palace: kg stats active: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT predicate_id FROM triples ORDER BY predicate_id")
	if err != nil {
		return nil, fmt.Errorf("palace: kg stats predicates: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("palace: scan predicate: %w", err)
		}
		stats.Predicates = append(stats.Predicates, p)
	}
	return stats, rows.Err()
}

// InsertDiaryEntry inserts a new diary entry.
func (s *SQLitePalaceStore) InsertDiaryEntry(ctx context.Context, d *DiaryEntry) error {
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO diary_entries (agent, entry, topic, created_at, created_at_epoch)
		 VALUES (?, ?, ?, ?, ?)`,
		d.Agent, d.Entry, d.Topic,
		d.CreatedAt.UTC().Format(time.RFC3339),
		d.CreatedAt.Unix())
	if err != nil {
		return fmt.Errorf("palace: insert diary entry: %w", err)
	}
	id, err := result.LastInsertId()
	if err == nil {
		d.ID = id
	}
	return nil
}

// ListDiaryEntries returns recent diary entries for an agent.
func (s *SQLitePalaceStore) ListDiaryEntries(ctx context.Context, agent string, limit int) ([]*DiaryEntry, error) {
	if limit <= 0 {
		limit = 10
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent, entry, topic, created_at FROM diary_entries
		 WHERE agent = ? ORDER BY created_at_epoch DESC LIMIT ?`,
		agent, limit)
	if err != nil {
		return nil, fmt.Errorf("palace: list diary entries: %w", err)
	}
	defer rows.Close()

	var entries []*DiaryEntry
	for rows.Next() {
		d := &DiaryEntry{}
		var createdAt string
		if err := rows.Scan(&d.ID, &d.Agent, &d.Entry, &d.Topic, &createdAt); err != nil {
			return nil, fmt.Errorf("palace: scan diary entry: %w", err)
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		entries = append(entries, d)
	}
	return entries, rows.Err()
}

// Close closes the underlying database connection.
func (s *SQLitePalaceStore) Close() error {
	return s.db.Close()
}

// scanDrawer scans a single drawer row.
func scanDrawer(row *sql.Row) (*Drawer, error) {
	d := &Drawer{}
	var blob []byte
	var createdAt string
	err := row.Scan(
		&d.ID, &d.Wing, &d.Room, &d.Hall,
		&d.Content, &d.SourceFile, &d.ChunkIndex,
		&d.AddedBy, &d.Importance, &blob, &createdAt,
	)
	if err != nil {
		return nil, err
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	d.Embedding = UnmarshalEmbedding(blob)
	return d, nil
}

// scanDrawers scans multiple drawer rows.
func scanDrawers(rows *sql.Rows) ([]*Drawer, error) {
	var drawers []*Drawer
	for rows.Next() {
		d := &Drawer{}
		var blob []byte
		var createdAt string
		if err := rows.Scan(
			&d.ID, &d.Wing, &d.Room, &d.Hall,
			&d.Content, &d.SourceFile, &d.ChunkIndex,
			&d.AddedBy, &d.Importance, &blob, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("palace: scan drawer: %w", err)
		}
		d.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		d.Embedding = UnmarshalEmbedding(blob)
		drawers = append(drawers, d)
	}
	return drawers, rows.Err()
}

// scanTriples scans triple rows from a query result.
func scanTriples(rows *sql.Rows) ([]*Triple, error) {
	var triples []*Triple
	for rows.Next() {
		t := &Triple{}
		var validFrom, validTo sql.NullString
		var extractedAt string
		if err := rows.Scan(
			&t.ID, &t.SubjectID, &t.PredicateID, &t.ObjectID,
			&validFrom, &validTo, &t.SourceFile, &extractedAt,
		); err != nil {
			return nil, fmt.Errorf("palace: scan triple: %w", err)
		}
		t.ExtractedAt, _ = time.Parse(time.RFC3339, extractedAt)
		if validFrom.Valid {
			parsed, _ := time.Parse(time.RFC3339, validFrom.String)
			t.ValidFrom = &parsed
		}
		if validTo.Valid {
			parsed, _ := time.Parse(time.RFC3339, validTo.String)
			t.ValidTo = &parsed
		}
		triples = append(triples, t)
	}
	return triples, rows.Err()
}
