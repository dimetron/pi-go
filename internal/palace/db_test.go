package palace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDBMemory(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB(:memory:): %v", err)
	}
	defer db.Close()

	// Verify migrations ran
	var version int
	err = db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_versions").Scan(&version)
	if err != nil {
		t.Fatalf("query schema version: %v", err)
	}
	if version != len(migrations) {
		t.Errorf("schema version = %d, want %d", version, len(migrations))
	}
}

func TestOpenDBFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "subdir", "palace.db")

	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatalf("OpenDB(%s): %v", dbPath, err)
	}
	defer db.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}
}

func TestMigrationsIdempotent(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	// Running migrate again should be a no-op
	if err := migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var version int
	_ = db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_versions").Scan(&version)
	if version != len(migrations) {
		t.Errorf("schema version after re-migrate = %d, want %d", version, len(migrations))
	}
}

func TestHasFTS5(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	if !HasFTS5(db) {
		t.Error("HasFTS5 returned false, expected true after migrations")
	}
}

func TestTablesExist(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	tables := []string{"drawers", "entities", "triples", "diary_entries", "schema_versions"}
	for _, table := range tables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}
