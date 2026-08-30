package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// readSessionEvents returns the events.jsonl bytes of a session dir.
func readSessionEvents(t *testing.T, root, id string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, id, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMergeEventsFiles_EmptyLocal(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, local, "s", "", "2026-01-01T00:00:00Z")
	writeSessionDir(t, remote, "s", "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")

	winner, err := (&mergeRunner{}).mergeEventsFiles(filepath.Join(local, "s"), filepath.Join(remote, "s"))
	if err != nil {
		t.Fatal(err)
	}
	if winner != "remote" {
		t.Fatalf("empty local should take remote, got %s", winner)
	}
	if got := readSessionEvents(t, local, "s"); !strings.Contains(got, "\"id\":\"a\"") {
		t.Fatalf("remote events not copied: %q", got)
	}
}

func TestMergeEventsFiles_EmptyRemote(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, local, "s", "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")
	writeSessionDir(t, remote, "s", "", "2026-01-01T00:00:00Z")

	winner, err := (&mergeRunner{}).mergeEventsFiles(filepath.Join(local, "s"), filepath.Join(remote, "s"))
	if err != nil {
		t.Fatal(err)
	}
	if winner != "local" {
		t.Fatalf("empty remote should keep local, got %s", winner)
	}
}

func TestMergeEventsFiles_RemoteContinuation(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, local, "s", "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")
	writeSessionDir(t, remote, "s", "{\"id\":\"a\"}\n{\"id\":\"b\"}\n", "2026-01-01T00:00:00Z")

	winner, err := (&mergeRunner{}).mergeEventsFiles(filepath.Join(local, "s"), filepath.Join(remote, "s"))
	if err != nil {
		t.Fatal(err)
	}
	if winner != "remote" {
		t.Fatalf("remote continuation should win, got %s", winner)
	}
	if got := readSessionEvents(t, local, "s"); got != "{\"id\":\"a\"}\n{\"id\":\"b\"}\n" {
		t.Fatalf("remote tail not appended: %q", got)
	}
}

func TestMergeEventsFiles_LocalContinuation(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, local, "s", "{\"id\":\"a\"}\n{\"id\":\"b\"}\n", "2026-01-01T00:00:00Z")
	writeSessionDir(t, remote, "s", "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")

	winner, err := (&mergeRunner{}).mergeEventsFiles(filepath.Join(local, "s"), filepath.Join(remote, "s"))
	if err != nil {
		t.Fatal(err)
	}
	if winner != "local" {
		t.Fatalf("local continuation should stay, got %s", winner)
	}
	if got := readSessionEvents(t, local, "s"); got != "{\"id\":\"a\"}\n{\"id\":\"b\"}\n" {
		t.Fatalf("local events must not be truncated: %q", got)
	}
}

func TestMergeEventsFiles_EqualHistoriesNoWrite(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	events := "{\"id\":\"a\"}\n{\"id\":\"b\"}\n"
	writeSessionDir(t, local, "s", events, "2026-01-01T00:00:00Z")
	writeSessionDir(t, remote, "s", events, "2026-01-01T00:00:00Z")

	winner, err := (&mergeRunner{}).mergeEventsFiles(filepath.Join(local, "s"), filepath.Join(remote, "s"))
	if err != nil {
		t.Fatal(err)
	}
	// Equal histories are NOT a continuation: nothing to append.
	if winner != "local" {
		t.Fatalf("equal histories must not be treated as continuation, got %s", winner)
	}
	if got := readSessionEvents(t, local, "s"); got != events {
		t.Fatalf("local events changed: %q", got)
	}
}

func TestMergeEventsFiles_DivergentAppendsRemoteOnly(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, local, "s", "{\"id\":\"a\"}\n{\"id\":\"b\"}\n", "2026-01-01T00:00:00Z")
	// Remote diverges: shares a, adds c. The remote-only event c is appended.
	writeSessionDir(t, remote, "s", "{\"id\":\"a\"}\n{\"id\":\"c\"}\n", "2026-01-01T00:00:00Z")

	winner, err := (&mergeRunner{}).mergeEventsFiles(filepath.Join(local, "s"), filepath.Join(remote, "s"))
	if err != nil {
		t.Fatal(err)
	}
	if winner != "merged" {
		t.Fatalf("divergent should merge, got %s", winner)
	}
	got := readSessionEvents(t, local, "s")
	if !strings.Contains(got, "\"id\":\"c\"") {
		t.Fatalf("remote-only event not appended: %q", got)
	}
	if !strings.Contains(got, "\"id\":\"b\"") {
		t.Fatalf("local-only event lost: %q", got)
	}
	if strings.Count(got, "\"id\":\"a\"") != 1 {
		t.Fatalf("shared event duplicated: %q", got)
	}
}

func TestMergeEventsFiles_UnparseableRemoteErrors(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, local, "s", "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")
	writeSessionDir(t, remote, "s", "not json\n", "2026-01-01T00:00:00Z")

	if _, err := (&mergeRunner{}).mergeEventsFiles(filepath.Join(local, "s"), filepath.Join(remote, "s")); err == nil {
		t.Fatal("unparseable remote events must be reported, not silently skipped")
	}
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
	if got := readSessionEvents(t, local, "260101-0000-aaaaa-11111"); got != "{\"id\":\"a\"}\n" {
		t.Fatalf("local-only session modified: %q", got)
	}
}

func TestMergeRemoteSessions_EventsOnlyLocalSessionIsMerged(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	// Local has events.jsonl but no meta.json (torn or legacy): it must be
	// merged, not treated as absent and overwritten.
	sid := "260101-0000-aaaaa-11111"
	localDir := filepath.Join(local, sid)
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "events.jsonl"), []byte("{\"id\":\"a\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSessionDir(t, remote, sid, "{\"id\":\"a\"}\n{\"id\":\"b\"}\n", "2026-01-01T00:00:00Z")

	report, err := MergeRemoteSessions(local, remote, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Merged != 1 || report.Added != 0 {
		t.Fatalf("events-only local session should be merged: %+v", report)
	}
	if got := readSessionEvents(t, local, sid); !strings.Contains(got, "\"id\":\"b\"") {
		t.Fatalf("local events lost in events-only merge: %q", got)
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
	got := readSessionEvents(t, local, "260101-0000-aaaaa-11111")
	if !strings.Contains(got, "\"id\":\"b\"") {
		t.Fatalf("remote continuation events missing after merge: %q", got)
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
	if got := readSessionEvents(t, local, "260101-0000-aaaaa-11111"); got != "{\"id\":\"a\",\"timestamp\":\"2026-01-01T00:00:00Z\"}\n" {
		t.Fatalf("dry-run modified local events: %q", got)
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

func TestMergeBranchesJSON_UnionPreservesLocalOnly(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	writeSessionDir(t, local, "s", "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")
	writeSessionDir(t, remote, "s", "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")
	// Local has branches: main (head 1) and local-only "l" (head 3).
	lb := branchState{Active: "l", Branches: map[string]BranchInfo{
		"main": {Name: "main", Head: 1},
		"l":    {Name: "l", Head: 3, Parent: ptr("main"), ForkPoint: 1},
	}}
	if err := saveBranches(filepath.Join(local, "s"), &lb); err != nil {
		t.Fatal(err)
	}
	// Remote has main (head 2 — further along) and remote-only "r".
	rb := branchState{Active: "r", Branches: map[string]BranchInfo{
		"main": {Name: "main", Head: 2},
		"r":    {Name: "r", Head: 1, Parent: ptr("main"), ForkPoint: 2},
	}}
	if err := saveBranches(filepath.Join(remote, "s"), &rb); err != nil {
		t.Fatal(err)
	}

	if _, err := MergeRemoteSessions(local, remote, MergeOptions{}); err != nil {
		t.Fatal(err)
	}
	merged, err := loadBranches(filepath.Join(local, "s"))
	if err != nil {
		t.Fatal(err)
	}
	// All three branches survive; main takes the larger head.
	if len(merged.Branches) != 3 {
		t.Fatalf("expected 3 branch entries, got %d: %+v", len(merged.Branches), merged.Branches)
	}
	if _, ok := merged.Branches["l"]; !ok {
		t.Fatalf("local-only branch dropped: %+v", merged.Branches)
	}
	if _, ok := merged.Branches["r"]; !ok {
		t.Fatalf("remote-only branch dropped: %+v", merged.Branches)
	}
	if merged.Branches["main"].Head != 2 {
		t.Fatalf("main head should take the larger value (2), got %d", merged.Branches["main"].Head)
	}
}

func TestMergeRemoteSessions_UnparseableRemoteReported(t *testing.T) {
	local := t.TempDir()
	remote := t.TempDir()
	sid := "260101-0000-aaaaa-11111"
	writeSessionDir(t, local, sid, "{\"id\":\"a\"}\n", "2026-01-01T00:00:00Z")
	// Corrupt remote events: must surface as a per-session error.
	rdir := filepath.Join(remote, sid)
	if err := os.MkdirAll(rdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rdir, "events.jsonl"), []byte("not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rdir, "meta.json"), []byte("{\"id\":\""+sid+"\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := MergeRemoteSessions(local, remote, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Errors) != 1 {
		t.Fatalf("expected 1 per-session error, got %+v", report)
	}
	if !strings.Contains(report.Errors[0], sid) {
		t.Fatalf("error should name the session: %q", report.Errors[0])
	}
}

func ptr(s string) *string { return &s }
