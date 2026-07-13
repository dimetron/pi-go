package palace

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// migrations is the ordered list of schema migrations.
var migrations = []string{
	// Version 1: Drawers table with FTS5 and triggers
	`
	CREATE TABLE IF NOT EXISTS drawers (
		id           TEXT PRIMARY KEY,
		wing         TEXT NOT NULL,
		room         TEXT NOT NULL,
		hall         TEXT NOT NULL DEFAULT '',
		content      TEXT NOT NULL,
		source_file  TEXT NOT NULL DEFAULT '',
		chunk_index  INTEGER NOT NULL DEFAULT 0,
		added_by     TEXT NOT NULL DEFAULT '',
		importance   INTEGER NOT NULL DEFAULT 0,
		embedding    BLOB,
		created_at   TEXT NOT NULL,
		created_at_epoch INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_drawers_wing ON drawers(wing);
	CREATE INDEX IF NOT EXISTS idx_drawers_room ON drawers(wing, room);
	CREATE INDEX IF NOT EXISTS idx_drawers_hall ON drawers(wing, room, hall);
	CREATE INDEX IF NOT EXISTS idx_drawers_created ON drawers(created_at_epoch DESC);
	CREATE INDEX IF NOT EXISTS idx_drawers_importance ON drawers(importance DESC);

	CREATE VIRTUAL TABLE IF NOT EXISTS drawers_fts USING fts5(
		content,
		content='drawers', content_rowid='rowid'
	);

	CREATE TRIGGER IF NOT EXISTS drawers_ai AFTER INSERT ON drawers BEGIN
		INSERT INTO drawers_fts(rowid, content)
		VALUES (new.rowid, new.content);
	END;
	CREATE TRIGGER IF NOT EXISTS drawers_ad AFTER DELETE ON drawers BEGIN
		INSERT INTO drawers_fts(drawers_fts, rowid, content)
		VALUES('delete', old.rowid, old.content);
	END;
	CREATE TRIGGER IF NOT EXISTS drawers_au AFTER UPDATE ON drawers BEGIN
		INSERT INTO drawers_fts(drawers_fts, rowid, content)
		VALUES('delete', old.rowid, old.content);
		INSERT INTO drawers_fts(rowid, content)
		VALUES (new.rowid, new.content);
	END;
	`,

	// Version 2: Entities and triples for the knowledge graph
	`
	CREATE TABLE IF NOT EXISTS entities (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		type       TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_entities_name ON entities(name);

	CREATE TABLE IF NOT EXISTS triples (
		id           TEXT PRIMARY KEY,
		subject_id   TEXT NOT NULL,
		predicate_id TEXT NOT NULL,
		object_id    TEXT NOT NULL,
		valid_from   TEXT,
		valid_to     TEXT,
		source_file  TEXT NOT NULL DEFAULT '',
		extracted_at TEXT NOT NULL,
		FOREIGN KEY(subject_id)   REFERENCES entities(id),
		FOREIGN KEY(object_id)    REFERENCES entities(id)
	);
	CREATE INDEX IF NOT EXISTS idx_triples_subject ON triples(subject_id);
	CREATE INDEX IF NOT EXISTS idx_triples_object ON triples(object_id);
	CREATE INDEX IF NOT EXISTS idx_triples_predicate ON triples(predicate_id);
	CREATE INDEX IF NOT EXISTS idx_triples_valid ON triples(valid_from, valid_to);
	`,

	// Version 3: Diary entries
	`
	CREATE TABLE IF NOT EXISTS diary_entries (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		agent      TEXT NOT NULL,
		entry      TEXT NOT NULL,
		topic      TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL,
		created_at_epoch INTEGER NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_diary_agent ON diary_entries(agent);
	CREATE INDEX IF NOT EXISTS idx_diary_created ON diary_entries(created_at_epoch DESC);
	`,

	// Version 4: Content hash, for incremental mining.
	//
	// A drawer's ID is md5(source_file:chunk_index) — deliberately stable across
	// edits, so an edited chunk replaces its predecessor instead of accumulating.
	// The consequence is that the ID says nothing about whether the content
	// changed, so every run re-embedded every chunk from scratch. Embedding is
	// ~80% of a mining run's CPU. Storing the content hash lets an unchanged
	// chunk be recognized and skipped before the embedder is ever called.
	//
	// Existing rows get '' and are treated as "hash unknown", so the first run
	// after this migration re-embeds once and records hashes as it goes.
	`
	ALTER TABLE drawers ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';
	`,
}

// OpenDB opens (or creates) a palace SQLite database at the given path with
// WAL mode and runs pending migrations. Pass ":memory:" for in-memory databases.
func OpenDB(dbPath string) (*sql.DB, error) {
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("palace: create dir %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("palace: open db: %w", err)
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA mmap_size=268435456", // 256MB
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("palace: %s: %w", p, err)
		}
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("palace: migrate: %w", err)
	}

	return db, nil
}

// migrate creates the schema_versions table and applies pending migrations.
func migrate(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_versions (
		id         INTEGER PRIMARY KEY,
		version    INTEGER UNIQUE NOT NULL,
		applied_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create schema_versions: %w", err)
	}

	var current int
	row := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_versions")
	if err := row.Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for i := current; i < len(migrations); i++ {
		version := i + 1
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", version, err)
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", version, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO schema_versions (version, applied_at) VALUES (?, ?)",
			version, time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", version, err)
		}
	}

	return nil
}

// HasFTS5 checks whether the database supports FTS5 for drawers.
func HasFTS5(db *sql.DB) bool {
	var dummy string
	err := db.QueryRow("SELECT 1 FROM drawers_fts LIMIT 0").Scan(&dummy)
	return err == nil || err == sql.ErrNoRows
}
