package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeBranch creates branches/<name>/events.jsonl under a session dir.
func writeBranch(t *testing.T, sessionDir, name, events string) {
	t.Helper()
	dir := filepath.Join(sessionDir, "branches", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mergeBranchesDir decides, per branch directory, whether to copy the remote
// branch in whole or to merge its events. It was 32% covered, so the branch
// decisions — the part that can silently lose a branch's events — were
// untested. The rule is: a branch with no local events.jsonl is copied in, and
// a branch present on both sides has its events appended.
func TestMergeBranchesDir(t *testing.T) {
	t.Run("remote-only branch is copied in whole", func(t *testing.T) {
		local, remote := t.TempDir(), t.TempDir()
		writeBranch(t, remote, "feature", `{"id":"a"}`+"\n")

		r := &mergeRunner{}
		if err := r.mergeBranchesDir(local, remote); err != nil {
			t.Fatalf("mergeBranchesDir: %v", err)
		}

		got, err := os.ReadFile(filepath.Join(local, "branches", "feature", "events.jsonl"))
		if err != nil {
			t.Fatalf("branch was not copied in: %v", err)
		}
		if string(got) != `{"id":"a"}`+"\n" {
			t.Errorf("copied events = %q", got)
		}
	})

	// The defining property of the branch merge is that local events are never
	// lost — the local file is only ever appended to. The two sides must
	// therefore diverge, with each holding an event the other lacks: if the
	// remote side were a superset of the local one, replace and append would
	// produce identical bytes and the test could not tell them apart. (It could
	// not, until this was changed: an earlier version used a local prefix and
	// passed against a replace-instead-of-append mutation.)
	t.Run("divergent branches union instead of replacing", func(t *testing.T) {
		local, remote := t.TempDir(), t.TempDir()
		writeBranch(t, local, "main", `{"id":"shared"}`+"\n"+`{"id":"local-only"}`+"\n")
		writeBranch(t, remote, "main", `{"id":"shared"}`+"\n"+`{"id":"remote-only"}`+"\n")

		r := &mergeRunner{}
		if err := r.mergeBranchesDir(local, remote); err != nil {
			t.Fatalf("mergeBranchesDir: %v", err)
		}

		got, err := os.ReadFile(filepath.Join(local, "branches", "main", "events.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		// The local event must survive the merge.
		if !strings.Contains(string(got), "local-only") {
			t.Errorf("the local-only event was lost; a merge must never rewrite the local file:\n%s", got)
		}
		// And the remote-only event must have been brought in.
		if !strings.Contains(string(got), "remote-only") {
			t.Errorf("the remote-only event was not merged in:\n%s", got)
		}
	})

	// Identical histories are a no-op: the local file must not be rewritten, or
	// a concurrent writer's append could be clobbered.
	t.Run("a branch already in sync is left alone", func(t *testing.T) {
		local, remote := t.TempDir(), t.TempDir()
		writeBranch(t, local, "main", `{"id":"a"}`+"\n")
		writeBranch(t, remote, "main", `{"id":"a"}`+"\n")

		r := &mergeRunner{}
		if err := r.mergeBranchesDir(local, remote); err != nil {
			t.Fatalf("mergeBranchesDir: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(local, "branches", "main", "events.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != `{"id":"a"}`+"\n" {
			t.Errorf("in-sync branch content changed: %q", got)
		}
	})

	// A local branch the remote side has never seen must survive untouched: the
	// merge only ever adds, and a branch dir the remote lacks is not in the
	// remote entries at all.
	t.Run("local-only branch is untouched", func(t *testing.T) {
		local, remote := t.TempDir(), t.TempDir()
		writeBranch(t, local, "local-only", `{"id":"keep"}`+"\n")
		writeBranch(t, remote, "other", `{"id":"x"}`+"\n")

		r := &mergeRunner{}
		if err := r.mergeBranchesDir(local, remote); err != nil {
			t.Fatalf("mergeBranchesDir: %v", err)
		}

		got, err := os.ReadFile(filepath.Join(local, "branches", "local-only", "events.jsonl"))
		if err != nil {
			t.Fatalf("local-only branch was removed: %v", err)
		}
		if string(got) != `{"id":"keep"}`+"\n" {
			t.Errorf("local-only branch was modified: %q", got)
		}
	})

	// Neither side having a branches/ dir is the overwhelmingly common case for
	// sessions that never branched, and must be a no-op rather than an error.
	t.Run("neither side has a branches dir", func(t *testing.T) {
		local, remote := t.TempDir(), t.TempDir()
		r := &mergeRunner{}
		if err := r.mergeBranchesDir(local, remote); err != nil {
			t.Errorf("mergeBranchesDir with no branches dirs: %v", err)
		}
	})

	// A local branches/ dir already exists, so the remote side is walked
	// per-entry rather than copied wholesale. A plain file there is not a branch
	// and must not be merged as one; only directories whose events.jsonl is
	// absent locally get copied.
	t.Run("a plain file in branches is skipped when local branches exist", func(t *testing.T) {
		local, remote := t.TempDir(), t.TempDir()
		for _, root := range []string{local, remote} {
			if err := os.MkdirAll(filepath.Join(root, "branches"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(remote, "branches", "notes.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}

		r := &mergeRunner{}
		if err := r.mergeBranchesDir(local, remote); err != nil {
			t.Fatalf("mergeBranchesDir: %v", err)
		}
		if _, err := os.Stat(filepath.Join(local, "branches", "notes.txt")); !os.IsNotExist(err) {
			t.Error("a non-directory entry was copied into the local branches dir")
		}
	})

	// With no local branches/ at all the remote dir is copied wholesale, which
	// does copy the file. That is the documented lMissing behavior and worth
	// stating, because it is the opposite of the per-entry path above.
	t.Run("no local branches dir copies the remote dir wholesale", func(t *testing.T) {
		local, remote := t.TempDir(), t.TempDir()
		writeBranch(t, remote, "feature", `{"id":"a"}`+"\n")

		r := &mergeRunner{}
		if err := r.mergeBranchesDir(local, remote); err != nil {
			t.Fatalf("mergeBranchesDir: %v", err)
		}
		if _, err := os.Stat(filepath.Join(local, "branches", "feature", "events.jsonl")); err != nil {
			t.Errorf("remote branches dir was not copied: %v", err)
		}
	})
}

// A dry run must report the same shape without writing: copyDir returns before
// creating anything, so a dry-run merge over a remote-only tree leaves the
// local tree empty.
func TestMergeBranchesDirDryRunWritesNothing(t *testing.T) {
	local, remote := t.TempDir(), t.TempDir()
	writeBranch(t, remote, "feature", `{"id":"a"}`+"\n")

	r := &mergeRunner{dryRun: true}
	if err := r.mergeBranchesDir(local, remote); err != nil {
		t.Fatalf("mergeBranchesDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(local, "branches", "feature")); !os.IsNotExist(err) {
		t.Error("a dry run created a branch directory")
	}
}

// isPrefix is the test that decides whether one side is a strict continuation
// of the other, which picks the append path. Its two rejections are what keep a
// divergent history from being treated as a continuation.
func TestIsPrefix(t *testing.T) {
	ev := func(ids ...string) []rawEvent {
		out := make([]rawEvent, len(ids))
		for i, id := range ids {
			out[i] = rawEvent{id: id}
		}
		return out
	}

	tests := []struct {
		name string
		a, b []rawEvent
		want bool
	}{
		{"empty is a prefix of anything", ev(), ev("a", "b"), true},
		{"equal slices are prefixes", ev("a", "b"), ev("a", "b"), true},
		{"strict prefix", ev("a"), ev("a", "b"), true},
		{"longer a is not a prefix", ev("a", "b", "c"), ev("a", "b"), false},
		{"divergent id is not a prefix", ev("a", "x"), ev("a", "b"), false},
		{"first id differs", ev("z"), ev("a"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPrefix(tc.a, tc.b); got != tc.want {
				t.Errorf("isPrefix(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// mergeMeta keeps the newer updatedAt. The branches worth pinning are the ones
// a hand-written merge gets wrong: a missing local meta takes the remote copy,
// and a missing remote meta leaves the local one alone.
func TestMergeMeta(t *testing.T) {
	writeMeta := func(t *testing.T, dir, updatedAt string) {
		t.Helper()
		content := `{"id":"s","updatedAt":"` + updatedAt + `"}` + "\n"
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name       string
		localAt    string // "" means no local meta.json
		remoteAt   string // "" means no remote meta.json
		wantWinner string
	}{
		{name: "remote is newer", localAt: "2026-01-01T00:00:00Z", remoteAt: "2026-06-01T00:00:00Z", wantWinner: "remote"},
		{name: "local is newer", localAt: "2026-06-01T00:00:00Z", remoteAt: "2026-01-01T00:00:00Z", wantWinner: "local"},
		{name: "equal timestamps keep local", localAt: "2026-01-01T00:00:00Z", remoteAt: "2026-01-01T00:00:00Z", wantWinner: "local"},
		{name: "no local meta takes remote", remoteAt: "2026-01-01T00:00:00Z", wantWinner: "remote"},
		{name: "no remote meta keeps local", localAt: "2026-01-01T00:00:00Z", wantWinner: "local"},
		{name: "neither side has meta", wantWinner: "local"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			local, remote := t.TempDir(), t.TempDir()
			if tc.localAt != "" {
				writeMeta(t, local, tc.localAt)
			}
			if tc.remoteAt != "" {
				writeMeta(t, remote, tc.remoteAt)
			}

			r := &mergeRunner{}
			winner, err := r.mergeMeta(local, remote)
			if err != nil {
				t.Fatalf("mergeMeta: %v", err)
			}
			if winner != tc.wantWinner {
				t.Errorf("winner = %q, want %q", winner, tc.wantWinner)
			}

			// The winning meta must actually be the one on disk.
			if tc.wantWinner == "remote" {
				if _, err := os.Stat(filepath.Join(local, "meta.json")); err != nil {
					t.Errorf("remote won but no local meta.json was written: %v", err)
				}
			}
		})
	}
}

// copyMeta is byte-for-byte on purpose: fields this binary does not know about
// have to survive a merge, so a decode-and-re-encode would silently drop them.
func TestCopyMetaPreservesUnknownFields(t *testing.T) {
	local, remote := t.TempDir(), t.TempDir()
	content := `{"id":"s","futureField":{"nested":true},"updatedAt":"2026-01-01T00:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(remote, "meta.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	r := &mergeRunner{}
	winner, err := r.copyMeta(local, remote)
	if err != nil {
		t.Fatalf("copyMeta: %v", err)
	}
	if winner != "remote" {
		t.Errorf("copyMeta returned %q, want %q", winner, "remote")
	}

	got, err := os.ReadFile(filepath.Join(local, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("meta was not copied byte-for-byte:\n got %q\nwant %q", got, content)
	}
}

// A missing remote meta.json is an error the caller has to see; returning a
// winner with no file written would report a successful merge that lost meta.
func TestCopyMetaMissingSourceErrors(t *testing.T) {
	local, remote := t.TempDir(), t.TempDir()
	r := &mergeRunner{}
	if _, err := r.copyMeta(local, remote); err == nil {
		t.Fatal("copyMeta with no remote meta.json returned no error")
	}
	if _, err := os.Stat(filepath.Join(local, "meta.json")); !os.IsNotExist(err) {
		t.Error("copyMeta wrote a local meta.json despite having no source")
	}
}
