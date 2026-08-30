package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeEventsData_EmptyLocal(t *testing.T) {
	remote := "{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n"
	merged, winner := mergeEventsData(nil, []byte(remote))
	if winner != "remote" || string(merged) != remote {
		t.Fatalf("empty local should take remote: winner=%s merged=%q", winner, merged)
	}
}

func TestMergeEventsData_EmptyRemote(t *testing.T) {
	local := "{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n"
	merged, winner := mergeEventsData([]byte(local), nil)
	if winner != "local" || string(merged) != local {
		t.Fatalf("empty remote should keep local: winner=%s merged=%q", winner, merged)
	}
}

func TestMergeEventsData_LocalPrefixOfRemote(t *testing.T) {
	local := "{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n"
	remote := local + "{\"id\":\"b\",\"timestamp\":\"2026-01-01T00:00:01Z\"}\n"
	merged, winner := mergeEventsData([]byte(local), []byte(remote))
	if winner != "remote" || string(merged) != remote {
		t.Fatalf("remote continuation should win: winner=%s merged=%q", winner, merged)
	}
}

func TestMergeEventsData_RemotePrefixOfLocal(t *testing.T) {
	local := "{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n{\"id\":\"b\",\"timestamp\":\"2026-01-01T00:00:01Z\"}\n"
	remote := "{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n"
	merged, winner := mergeEventsData([]byte(local), []byte(remote))
	if winner != "local" || string(merged) != local {
		t.Fatalf("local continuation should win: winner=%s merged=%q", winner, merged)
	}
}

func TestMergeEventsData_Divergent(t *testing.T) {
	local := "{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n{\"id\":\"b\",\"timestamp\":\"2026-01-01T00:00:02Z\"}\n"
	remote := "{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n{\"id\":\"c\",\"timestamp\":\"2026-01-01T00:00:01Z\"}\n"
	merged, winner := mergeEventsData([]byte(local), []byte(remote))
	if winner != "merged" {
		t.Fatalf("divergent should merge: winner=%s", winner)
	}
	// Union by ID, ordered by timestamp: a, c, b.
	want := []string{"\"id\":\"a\"", "\"id\":\"c\"", "\"id\":\"b\""}
	got := strings.Split(strings.TrimSpace(string(merged)), "\n")
	if len(got) != len(want) {
		t.Fatalf("expected %d events, got %d: %q", len(want), len(got), merged)
	}
	for i, w := range want {
		if !strings.Contains(got[i], w) {
			t.Fatalf("event %d should contain %s, got %q", i, w, got[i])
		}
	}
}

func TestMergeEventsData_UnparseableKeepsLocal(t *testing.T) {
	local := "{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n"
	remote := "not json\n"
	merged, winner := mergeEventsData([]byte(local), []byte(remote))
	if winner != "local" || string(merged) != local {
		t.Fatalf("unparseable remote should keep local: winner=%s", winner)
	}
}

// writeSessionDir creates a session dir with the given events and meta.
func writeSessionDir(t *testing.T, root, id, events, updatedAt string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if events != "" {
		if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	meta := "{\"id\":\"" + id + "\",\"appName\":\"pi-go\",\"userID\":\"local\",\"updatedAt\":\"" + updatedAt + "\"}\n"
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestMergeRemoteSessions_AddsNew(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, remote, "260101-0000-aaaaa-11111", "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")

	report, err := MergeRemoteSessions(local, remote, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 1 || report.Merged != 0 {
		t.Fatalf("expected 1 added, got added=%d merged=%d", report.Added, report.Merged)
	}
	if _, err := os.Stat(filepath.Join(local, "260101-0000-aaaaa-11111", "events.jsonl")); err != nil {
		t.Fatalf("new session not copied: %v", err)
	}
}

func TestMergeRemoteSessions_KeepsLocalOnly(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, local, "260101-0000-aaaaa-11111", "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")

	report, err := MergeRemoteSessions(local, remote, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 0 || report.Merged != 0 {
		t.Fatalf("local-only session must be untouched: added=%d merged=%d", report.Added, report.Merged)
	}
	data, err := os.ReadFile(filepath.Join(local, "260101-0000-aaaaa-11111", "events.jsonl"))
	if err != nil || string(data) != "{\"id\":\"a\"}\n" {
		t.Fatalf("local-only session was modified: %q %v", data, err)
	}
}

func TestMergeRemoteSessions_MergesContinuation(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, local, "260101-0000-aaaaa-11111",
		"{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n",
		"2026-01-01T00:00:00Z")
	writeSessionDir(t, remote, "260101-0000-aaaaa-11111",
		"{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n{\"id\":\"b\",\"timestamp\":\"2026-01-01T00:00:01Z\"}\n",
		"2026-01-01T00:00:01Z")

	report, err := MergeRemoteSessions(local, remote, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Merged != 1 {
		t.Fatalf("expected 1 merged, got %d", report.Merged)
	}
	data, err := os.ReadFile(filepath.Join(local, "260101-0000-aaaaa-11111", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"id\":\"b\"") {
		t.Fatalf("remote continuation events missing after merge: %q", data)
	}
	// meta should be the newer remote one
	meta, err := os.ReadFile(filepath.Join(local, "260101-0000-aaaaa-11111", "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(meta), "2026-01-01T00:00:01Z") {
		t.Fatalf("meta should be the newer remote copy: %q", meta)
	}
}

func TestMergeRemoteSessions_DryRunWritesNothing(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, local, "260101-0000-aaaaa-11111",
		"{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n",
		"2026-01-01T00:00:00Z")
	writeSessionDir(t, remote, "260101-0000-aaaaa-11111",
		"{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n{\"id\":\"b\",\"timestamp\":\"2026-01-01T00:00:01Z\"}\n",
		"2026-01-01T00:00:01Z")
	writeSessionDir(t, remote, "260101-0000-bbbbb-22222", "{\"id\":\"x\"}\n", "2026-01-01T00:00:00Z")

	report, err := MergeRemoteSessions(local, remote, MergeOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 1 || report.Merged != 1 {
		t.Fatalf("dry-run should still report: added=%d merged=%d", report.Added, report.Merged)
	}
	// Nothing written: local session unchanged, new session absent.
	data, err := os.ReadFile(filepath.Join(local, "260101-0000-aaaaa-11111", "events.jsonl"))
	if err != nil || string(data) != "{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n" {
		t.Fatalf("dry-run modified local events: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(local, "260101-0000-bbbbb-22222")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created a new session dir")
	}
}

func TestMergeRemoteSessions_NewerMetaWins(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, local, "260101-0000-aaaaa-11111", "{\"id\":\"a\"}\n", "2026-01-02T00:00:00Z")
	writeSessionDir(t, remote, "260101-0000-aaaaa-11111", "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")

	if _, err := MergeRemoteSessions(local, remote, MergeOptions{}); err != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile(filepath.Join(local, "260101-0000-aaaaa-11111", "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(meta), "2026-01-02T00:00:00Z") {
		t.Fatalf("newer local meta should win: %q", meta)
	}
}

func TestMergeRemoteSessions_IgnoresNonSessionFiles(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	// Junk at the sessions root must be ignored.
	if err := os.WriteFile(filepath.Join(remote, ".DS_Store"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(remote, "acp-server.err.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := MergeRemoteSessions(local, remote, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 0 || report.Merged != 0 || len(report.Errors) != 0 {
		t.Fatalf("junk files must be ignored: %+v", report)
	}
}

func TestMergeRemoteSessions_SkipsNonSessionDirs(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	// archive/ and backup/ hold archived sessions, not sessions themselves.
	for _, d := range []string{"archive", "backup", ".idea"} {
		if err := os.MkdirAll(filepath.Join(remote, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeSessionDir(t, remote, "260101-0000-aaaaa-11111", "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")

	report, err := MergeRemoteSessions(local, remote, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 1 || report.Merged != 0 {
		t.Fatalf("only the real session should be added: %+v", report)
	}
	for _, d := range []string{"archive", "backup", ".idea"} {
		if _, err := os.Stat(filepath.Join(local, d)); !os.IsNotExist(err) {
			t.Fatalf("non-session dir %s must not be copied", d)
		}
	}
}

func TestMergeRemoteSessions_TimeOrdering(t *testing.T) {
	// Ensure the divergent merge orders by timestamp, not by input order.
	local := "{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n{\"id\":\"b\",\"timestamp\":\"2026-01-01T00:00:05Z\"}\n"
	remote := "{\"id\":\"c\",\"timestamp\":\"2026-01-01T00:00:01Z\"}\n{\"id\":\"d\",\"timestamp\":\"2026-01-01T00:00:02Z\"}\n"
	merged, winner := mergeEventsData([]byte(local), []byte(remote))
	if winner != "merged" {
		t.Fatalf("expected merged, got %s", winner)
	}
	lines := strings.Split(strings.TrimSpace(string(merged)), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 events, got %d", len(lines))
	}
	// a(0) c(1) d(2) b(5)
	want := []string{"\"id\":\"a\"", "\"id\":\"c\"", "\"id\":\"d\"", "\"id\":\"b\""}
	for i, w := range want {
		if !strings.Contains(lines[i], w) {
			t.Fatalf("line %d should contain %s, got %q", i, w, lines[i])
		}
	}
}
