package cli

import (
	"context"
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

func TestNewMemoryKGQueryCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "palace.db")
	cmd := newMemoryKGQueryCmd()
	cmd.SetArgs([]string{"SomeEntity", "--db", dbPath})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryKGAddCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "palace.db")
	cmd := newMemoryKGAddCmd()
	cmd.SetArgs([]string{"Alice", "works_on", "api", "--db", dbPath})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryKGTimelineCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "palace.db")
	cmd := newMemoryKGTimelineCmd()
	cmd.SetArgs([]string{"Alice", "--db", dbPath})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryKGCmd_Subcommands(t *testing.T) {
	cmd := newMemoryKGCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"query", "add", "timeline"} {
		if !names[want] {
			t.Errorf("kg subcommand %q missing", want)
		}
	}
}

func TestOpenPalaceDB_InvalidPath(t *testing.T) {
	// Non-existent directory with no parent creation should fail.
	_, err := openPalaceDB("/nonexistent/dir/that/does/not/exist/palace.db")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestRunMemoryKGAdd_InvalidDate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")
	// Create DB
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	p.Close()

	err = runMemoryKGAdd("Alice", "works_on", "api", dbPath, "not-a-date")
	if err == nil {
		t.Error("expected error for invalid date")
	}
}

func TestRunMemoryKGQuery_WithAsOf(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	p.KGAdd(context.Background(), palace.TripleInput{
		Subject: "Alice", Predicate: "works_on", Object: "api",
	})
	p.Close()

	err = runMemoryKGQuery("Alice", dbPath, "2025-01-01", "")
	if err != nil {
		t.Fatalf("query with as-of: %v", err)
	}
}

func TestRunMemoryKGQuery_SubjectDirection(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	p.KGAdd(context.Background(), palace.TripleInput{
		Subject: "Alice", Predicate: "works_on", Object: "api",
	})
	p.Close()

	err = runMemoryKGQuery("Alice", dbPath, "", "subject")
	if err != nil {
		t.Fatalf("query with direction: %v", err)
	}
}

func TestRunMemoryKGTimeline_NoTriples(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	p.Close()

	err = runMemoryKGTimeline("NonexistentEntity", dbPath)
	if err != nil {
		t.Fatalf("timeline with no triples: %v", err)
	}
}

func TestOpenPalaceDB_ValidPath(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	// Create DB first.
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("creating palace: %v", err)
	}
	p.Close()

	// Now open it through the function.
	p2, err := openPalaceDB(dbPath)
	if err != nil {
		t.Fatalf("openPalaceDB: %v", err)
	}
	p2.Close()
}

func TestOpenPalaceDB_EmptyPathUsesDefault(t *testing.T) {
	// With empty path, uses ".pi-go/palace.db" relative to CWD.
	// May or may not exist. Test that the function doesn't panic.
	_, err := openPalaceDB("")
	if err == nil {
		t.Log("openPalaceDB succeeded with empty path (default DB exists)")
	}
}

func TestParseDate_Invalid(t *testing.T) {
	_, err := parseDate("not-a-date")
	if err == nil {
		t.Error("expected error for invalid date")
	}
}

func TestParseDate_InvalidFormat(t *testing.T) {
	_, err := parseDate("01-02-2024")
	if err == nil {
		t.Error("expected error for invalid format")
	}
}

func TestNewMemoryKGQueryCmd_Flags(t *testing.T) {
	cmd := newMemoryKGQueryCmd()
	for _, name := range []string{"db", "as-of", "direction"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not found", name)
		}
	}
}

func TestNewMemoryKGAddCmd_Flags(t *testing.T) {
	cmd := newMemoryKGAddCmd()
	if cmd.Flags().Lookup("db") == nil {
		t.Error("missing --db flag")
	}
	if cmd.Flags().Lookup("valid-from") == nil {
		t.Error("missing --valid-from flag")
	}
}

func TestNewMemoryKGTimelineCmd_Flag(t *testing.T) {
	cmd := newMemoryKGTimelineCmd()
	if cmd.Flags().Lookup("db") == nil {
		t.Error("missing --db flag")
	}
}

func TestTruncate_TableDriven(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"over limit", "hello world", 5, "hello..."},
		{"empty string", "", 5, ""},
		{"zero limit", "hello", 0, "..."},
		{"single char limit", "hello", 1, "h..."},
		{"two char limit", "hello", 2, "he..."},
		{"three char limit", "hello", 3, "hel..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}
