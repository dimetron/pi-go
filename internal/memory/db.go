package memory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// migrations is the ordered list of schema migrations.
// Each entry is a SQL statement to execute for that version.
var migrations = []string{
	// Version 1: Core tables and indexes
	`
	CREATE TABLE IF NOT EXISTS sessions (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id      TEXT UNIQUE NOT NULL,
		project         TEXT NOT NULL,
		user_prompt     TEXT,
		started_at      TEXT NOT NULL,
		started_at_epoch INTEGER NOT NULL,
		completed_at    TEXT,
		completed_at_epoch INTEGER,
		status          TEXT CHECK(status IN ('active', 'completed', 'failed')) NOT NULL DEFAULT 'active'
	);
	CREATE INDEX IF NOT EXISTS idx_sessions_project ON sessions(project);
	CREATE INDEX IF NOT EXISTS idx_sessions_started ON sessions(started_at_epoch DESC);

	CREATE TABLE IF NOT EXISTS observations (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id      TEXT NOT NULL,
		project         TEXT NOT NULL,
		title           TEXT,
		type            TEXT NOT NULL CHECK(type IN ('decision', 'bugfix', 'feature', 'refactor', 'discovery', 'change')),
		text            TEXT,
		source_files    TEXT,
		tool_name       TEXT,
		prompt_number   INTEGER,
		discovery_tokens INTEGER DEFAULT 0,
		created_at      TEXT NOT NULL,
		created_at_epoch INTEGER NOT NULL,
		FOREIGN KEY(session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_obs_session ON observations(session_id);
	CREATE INDEX IF NOT EXISTS idx_obs_project ON observations(project);
	CREATE INDEX IF NOT EXISTS idx_obs_type ON observations(type);
	CREATE INDEX IF NOT EXISTS idx_obs_created ON observations(created_at_epoch DESC);
	CREATE INDEX IF NOT EXISTS idx_obs_project_created ON observations(project, created_at_epoch DESC);

	CREATE TABLE IF NOT EXISTS session_summaries (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		session_id      TEXT UNIQUE NOT NULL,
		project         TEXT NOT NULL,
		request         TEXT,
		investigated    TEXT,
		learned         TEXT,
		completed       TEXT,
		next_steps      TEXT,
		discovery_tokens INTEGER DEFAULT 0,
		created_at      TEXT NOT NULL,
		created_at_epoch INTEGER NOT NULL,
		FOREIGN KEY(session_id) REFERENCES sessions(session_id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_sum_project ON session_summaries(project);
	CREATE INDEX IF NOT EXISTS idx_sum_created ON session_summaries(created_at_epoch DESC);
	`,

	// Version 2: FTS5 virtual tables and sync triggers
	`
	CREATE VIRTUAL TABLE IF NOT EXISTS observations_fts USING fts5(
		title, text, source_files,
		content='observations', content_rowid='id'
	);

	CREATE TRIGGER IF NOT EXISTS observations_ai AFTER INSERT ON observations BEGIN
		INSERT INTO observations_fts(rowid, title, text, source_files)
		VALUES (new.id, new.title, new.text, new.source_files);
	END;
	CREATE TRIGGER IF NOT EXISTS observations_ad AFTER DELETE ON observations BEGIN
		INSERT INTO observations_fts(observations_fts, rowid, title, text, source_files)
		VALUES('delete', old.id, old.title, old.text, old.source_files);
	END;
	CREATE TRIGGER IF NOT EXISTS observations_au AFTER UPDATE ON observations BEGIN
		INSERT INTO observations_fts(observations_fts, rowid, title, text, source_files)
		VALUES('delete', old.id, old.title, old.text, old.source_files);
		INSERT INTO observations_fts(rowid, title, text, source_files)
		VALUES (new.id, new.title, new.text, new.source_files);
	END;

	CREATE VIRTUAL TABLE IF NOT EXISTS session_summaries_fts USING fts5(
		request, investigated, learned, completed, next_steps,
		content='session_summaries', content_rowid='id'
	);

	CREATE TRIGGER IF NOT EXISTS session_summaries_ai AFTER INSERT ON session_summaries BEGIN
		INSERT INTO session_summaries_fts(rowid, request, investigated, learned, completed, next_steps)
		VALUES (new.id, new.request, new.investigated, new.learned, new.completed, new.next_steps);
	END;
	CREATE TRIGGER IF NOT EXISTS session_summaries_ad AFTER DELETE ON session_summaries BEGIN
		INSERT INTO session_summaries_fts(session_summaries_fts, rowid, request, investigated, learned, completed, next_steps)
		VALUES('delete', old.id, old.request, old.investigated, old.learned, old.completed, old.next_steps);
	END;
	CREATE TRIGGER IF NOT EXISTS session_summaries_au AFTER UPDATE ON session_summaries BEGIN
		INSERT INTO session_summaries_fts(session_summaries_fts, rowid, request, investigated, learned, completed, next_steps)
		VALUES('delete', old.id, old.request, old.investigated, old.learned, old.completed, old.next_steps);
		INSERT INTO session_summaries_fts(rowid, request, investigated, learned, completed, next_steps)
		VALUES (new.id, new.request, new.investigated, new.learned, new.completed, new.next_steps);
	END;
	`,
}

// OpenDB opens (or creates) a SQLite database at the given path with WAL mode
// and runs pending migrations. Pass ":memory:" for in-memory databases.
func OpenDB(dbPath string) (*sql.DB, error) {
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("memory: create dir %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("memory: open db: %w", err)
	}

	// Enable WAL mode and foreign keys.
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA mmap_size=268435456", // 256MB
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("memory: %s: %w", p, err)
		}
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("memory: migrate: %w", err)
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

// HasFTS5 checks whether the database supports FTS5.
func HasFTS5(db *sql.DB) bool {
	var dummy string
	err := db.QueryRow("SELECT 1 FROM observations_fts LIMIT 0").Scan(&dummy)
	// If the table doesn't exist or FTS5 is unavailable, this will error.
	// A "no rows" result means the table exists and FTS5 works.
	return err == nil || err == sql.ErrNoRows
}
