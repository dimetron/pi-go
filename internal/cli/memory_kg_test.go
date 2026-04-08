package cli

import (
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/palace"
)

func TestMemoryKGCmd_SubcommandsRegistered(t *testing.T) {
	cmd := newMemoryKGCmd()

	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Name()] = true
	}

	for _, name := range []string{"query", "add", "timeline"} {
		if !subcommands[name] {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestMemoryKG_AddAndQuery(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	// Create palace DB.
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	p.Close()

	// Add a triple.
	err = runMemoryKGAdd("Alice", "works_on", "auth-migration", dbPath, "")
	if err != nil {
		t.Fatalf("add error: %v", err)
	}

	// Query it back.
	err = runMemoryKGQuery("Alice", dbPath, "", "")
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
}

func TestMemoryKG_AddWithValidFrom(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	p.Close()

	err = runMemoryKGAdd("Bob", "assigned_to", "billing", dbPath, "2026-03-01")
	if err != nil {
		t.Fatalf("add error: %v", err)
	}
}

func TestMemoryKG_Timeline(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	p.Close()

	// Add facts then view timeline.
	_ = runMemoryKGAdd("Alice", "works_on", "auth", dbPath, "2026-01-15")
	_ = runMemoryKGAdd("Alice", "assigned_to", "billing", dbPath, "2026-03-01")

	err = runMemoryKGTimeline("Alice", dbPath)
	if err != nil {
		t.Fatalf("timeline error: %v", err)
	}
}

func TestMemoryKG_QueryEmpty(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	p.Close()

	err = runMemoryKGQuery("nonexistent", dbPath, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		input string
		year  int
		month int
		day   int
	}{
		{"2026-03-15", 2026, 3, 15},
		{"2026-03-15T10:00:00Z", 2026, 3, 15},
	}

	for _, tt := range tests {
		parsed, err := parseDate(tt.input)
		if err != nil {
			t.Errorf("parseDate(%q): %v", tt.input, err)
			continue
		}
		if parsed.Year() != tt.year || int(parsed.Month()) != tt.month || parsed.Day() != tt.day {
			t.Errorf("parseDate(%q) = %v, want %d-%02d-%02d", tt.input, parsed, tt.year, tt.month, tt.day)
		}
	}
}

func TestTruncateCol(t *testing.T) {
	if got := truncateCol("short", 20); got != "short" {
		t.Errorf("truncateCol(short, 20) = %q", got)
	}
	if got := truncateCol("this is a very long string", 10); got != "this is a…" {
		t.Errorf("truncateCol(long, 10) = %q", got)
	}
}
