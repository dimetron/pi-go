package memory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestOpenDB_Memory(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB(:memory:): %v", err)
	}
	defer db.Close()

	// Verify WAL mode
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	// In-memory databases may report "memory" instead of "wal"
	if journalMode != "wal" && journalMode != "memory" {
		t.Errorf("journal_mode = %q, want wal or memory", journalMode)
	}

	// Verify foreign keys enabled
	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestOpenDB_CreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "nested", "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB(%s): %v", dbPath, err)
	}
	defer db.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Errorf("expected database file to exist at %s", dbPath)
	}
}

func TestMigrations_CreateAllTables(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	tables := []string{"sessions", "observations", "session_summaries", "schema_versions"}
	for _, table := range tables {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestMigrations_FTS5Tables(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	ftsTables := []string{"observations_fts", "session_summaries_fts"}
	for _, table := range ftsTables {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table,
		).Scan(&name)
		if err != nil {
			t.Errorf("FTS5 table %q not found: %v", table, err)
		}
	}
}

func TestMigrations_Idempotent(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("first OpenDB: %v", err)
	}

	// Run migrations again on the same DB
	if err := migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	// Verify schema version is still correct
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_versions").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != len(migrations) {
		t.Errorf("version = %d, want %d", version, len(migrations))
	}

	db.Close()
}

func TestMigrations_SchemaVersionTracking(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_versions").Scan(&count); err != nil {
		t.Fatalf("count schema_versions: %v", err)
	}
	if count != len(migrations) {
		t.Errorf("schema_versions count = %d, want %d", count, len(migrations))
	}
}

func TestHasFTS5(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	if !HasFTS5(db) {
		t.Error("HasFTS5 returned false, want true")
	}
}

func TestHasFTS5_NoFTS(t *testing.T) {
	// Open a plain DB without migrations to simulate no FTS5 tables
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	if HasFTS5(db) {
		t.Error("HasFTS5 returned true on DB without FTS tables, want false")
	}
}

func TestMigrations_Triggers(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	expectedTriggers := []string{
		"observations_ai", "observations_ad", "observations_au",
		"session_summaries_ai", "session_summaries_ad", "session_summaries_au",
	}
	for _, trig := range expectedTriggers {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='trigger' AND name=?", trig,
		).Scan(&name)
		if err != nil {
			t.Errorf("trigger %q not found: %v", trig, err)
		}
	}
}

func TestOpenDB_ReadOnlyDirFails(t *testing.T) {
	// Try to open a DB inside a path that cannot be created (file used as dir).
	f, err := os.CreateTemp(t.TempDir(), "blocker-*")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	f.Close()
	// Use the file (not a dir) as the parent directory — MkdirAll should fail.
	badPath := filepath.Join(f.Name(), "subdir", "test.db")
	_, err = OpenDB(badPath)
	if err == nil {
		t.Error("OpenDB with bad path: expected error, got nil")
	}
}

func TestMigrate_Partial(t *testing.T) {
	// Open without migrations (raw sql.Open), manually apply first migration only,
	// then call migrate() — it should apply only the remaining one.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	// Bootstrap schema_versions table and apply first migration manually.
	db.Exec(`CREATE TABLE IF NOT EXISTS schema_versions (
		id INTEGER PRIMARY KEY, version INTEGER UNIQUE NOT NULL, applied_at TEXT NOT NULL)`)
	db.Exec(migrations[0])
	db.Exec(`INSERT INTO schema_versions (version, applied_at) VALUES (1, '2026-01-01T00:00:00Z')`)

	// Now call migrate — should apply only migration 2.
	if err := migrate(db); err != nil {
		t.Fatalf("migrate on partial DB: %v", err)
	}

	var version int
	db.QueryRow("SELECT MAX(version) FROM schema_versions").Scan(&version)
	if version != len(migrations) {
		t.Errorf("version = %d, want %d", version, len(migrations))
	}
}

func TestOpenDB_FileBased(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}

	// Verify WAL mode on file-based DB
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	db.Close()

	// Reopen and verify migrations don't re-run
	db2, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("reopen OpenDB: %v", err)
	}
	defer db2.Close()

	var version int
	if err := db2.QueryRow("SELECT MAX(version) FROM schema_versions").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != len(migrations) {
		t.Errorf("version = %d after reopen, want %d", version, len(migrations))
	}
}

// Pragmas must hold on every connection the pool opens, not just the first.
//
// Executing them once on the pool configures whichever single connection served
// the statement; database/sql then hands out fresh, unconfigured connections
// under load. With busy_timeout=0 on those, a concurrent writer gets
// SQLITE_BUSY immediately instead of waiting, and the write is lost — measured
// at 5 of 12 concurrent CreateSession calls surviving.
//
// Connections are checked one at a time: the pool is deliberately bounded, so
// holding several open at once would block here rather than prove anything.
func TestOpenDB_PragmasApplyToEveryConnection(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	ctx := context.Background()
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn %d: %v", i, err)
		}
		var busy, fk int
		var journal string
		if err := c.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busy); err != nil {
			c.Close()
			t.Fatalf("conn %d busy_timeout: %v", i, err)
		}
		if err := c.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
			c.Close()
			t.Fatalf("conn %d foreign_keys: %v", i, err)
		}
		c.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal)
		c.Close()

		if busy <= 0 {
			t.Errorf("conn %d busy_timeout = %d, want > 0 — a concurrent write will fail instead of waiting", i, busy)
		}
		if fk != 1 {
			t.Errorf("conn %d foreign_keys = %d, want 1", i, fk)
		}
		seen[journal]++
	}
	if len(seen) != 1 {
		t.Errorf("journal_mode differs across connections: %v", seen)
	}
}

// Concurrent creates must all persist. This is the user-visible consequence of
// the pragma bug above: the row is silently dropped.
func TestSQLiteStore_ConcurrentCreateSessionPersistsAll(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()
	store := NewSQLiteStore(db)
	ctx := context.Background()

	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = store.CreateSession(ctx, &Session{
				SessionID: fmt.Sprintf("s-%02d", i),
				Project:   "/project",
				StartedAt: time.Now(),
				Status:    "active",
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("CreateSession %d: %v", i, err)
		}
	}

	var rows int
	if err := db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&rows); err != nil {
		t.Fatalf("count: %v", err)
	}
	if rows != n {
		t.Errorf("persisted %d of %d sessions — concurrent creates were lost", rows, n)
	}
}
