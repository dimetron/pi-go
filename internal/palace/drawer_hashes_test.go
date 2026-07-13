package palace

import (
	"context"
	"strings"
	"testing"
)

// DrawerHashes is what makes mining incremental: the miner compares these
// against the hashes of the chunks it just read and only re-embeds what differs.
func TestDrawerHashes(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	store := NewSQLitePalaceStore(db)
	ctx := context.Background()

	if err := store.InsertDrawer(ctx, &Drawer{
		ID: "d1", Wing: "backend", Room: "auth", Content: "token check", ContentHash: "h1",
	}); err != nil {
		t.Fatalf("InsertDrawer: %v", err)
	}
	if err := store.InsertDrawer(ctx, &Drawer{
		ID: "d2", Wing: "frontend", Room: "ui", Content: "button", ContentHash: "h2",
	}); err != nil {
		t.Fatalf("InsertDrawer: %v", err)
	}

	hashes, err := store.DrawerHashes(ctx, "backend")
	if err != nil {
		t.Fatalf("DrawerHashes: %v", err)
	}
	// Scoped to the wing: a drawer in another wing must not appear.
	if len(hashes) != 1 {
		t.Fatalf("got %d hashes, want 1: %v", len(hashes), hashes)
	}
	if hashes["d1"] != "h1" {
		t.Errorf("hashes[d1] = %q, want %q", hashes["d1"], "h1")
	}
}

func TestDrawerHashesEmptyWing(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	hashes, err := NewSQLitePalaceStore(db).DrawerHashes(context.Background(), "nothing-here")
	if err != nil {
		t.Fatalf("DrawerHashes: %v", err)
	}
	if len(hashes) != 0 {
		t.Errorf("got %d hashes for an unmined wing, want 0", len(hashes))
	}
}

// A store lookup failure is not fatal to mining — the miner falls back to
// re-embedding everything — but it must surface as an error rather than an
// empty, silently-complete hash set, which would look like "nothing changed".
func TestDrawerHashesQueryError(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`DROP TABLE drawers`); err != nil {
		t.Fatalf("dropping drawers: %v", err)
	}

	hashes, err := NewSQLitePalaceStore(db).DrawerHashes(context.Background(), "backend")
	if err == nil {
		t.Fatalf("DrawerHashes on a missing table returned %v, want error", hashes)
	}
	if !strings.Contains(err.Error(), "drawer hashes") {
		t.Errorf("error = %q, want it to name the operation", err)
	}
}

// Rows written before the content_hash migration can hold NULL, which will not
// scan into a string. That must be reported, not skipped.
func TestDrawerHashesScanError(t *testing.T) {
	t.Parallel()

	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	// Rebuild `drawers` without the NOT NULL guard so a legacy NULL hash can be
	// planted, then plant one.
	for _, stmt := range []string{
		`DROP TABLE drawers`,
		`CREATE TABLE drawers (id TEXT, wing TEXT, content_hash TEXT)`,
		`INSERT INTO drawers (id, wing, content_hash) VALUES ('d1', 'backend', NULL)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	hashes, err := NewSQLitePalaceStore(db).DrawerHashes(context.Background(), "backend")
	if err == nil {
		t.Fatalf("DrawerHashes over a NULL hash returned %v, want error", hashes)
	}
	if !strings.Contains(err.Error(), "scan drawer hash") {
		t.Errorf("error = %q, want it to name the scan failure", err)
	}
}
