package palace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin the behavior of the helpers extracted out of
// (*DrawerService).Search, MineConversations and extractTriples. Each case
// corresponds to a branch that used to live inline in those functions, so a
// regression in the extraction shows up here rather than in an integration
// test that happens to exercise the same path.

// --- drawer_service: FTS5 scoring ------------------------------------------

func TestMaxFTSRank(t *testing.T) {
	tests := []struct {
		name    string
		results []SearchResult
		want    int
	}{
		{"no results floors at 1", nil, 1},
		{"empty slice floors at 1", []SearchResult{}, 1},
		{"negative ranks never lower the floor", []SearchResult{{Rank: -8}, {Rank: -1}}, 1},
		{"zero rank never lowers the floor", []SearchResult{{Rank: 0}}, 1},
		{"largest positive rank wins", []SearchResult{{Rank: 2}, {Rank: 9}, {Rank: 5}}, 9},
		{"mixed signs take the maximum", []SearchResult{{Rank: -4}, {Rank: 3}}, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := maxFTSRank(tc.results); got != tc.want {
				t.Errorf("maxFTSRank = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestFTSRelevance(t *testing.T) {
	tests := []struct {
		name          string
		rank, maxRank int
		want          float64
	}{
		{"zero maxRank falls back to the midpoint", 0, 0, 0.5},
		{"negative maxRank falls back to the midpoint", -3, -1, 0.5},
		{"rank equal to maxRank scores the midpoint", 4, 4, 0.5},
		{"rank of zero against maxRank 1 scores the top", 0, 1, 1.0},
		{"more negative rank scores higher", -1, 1, 1.5},
		{"halfway between", 2, 4, 0.75},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ftsRelevance(tc.rank, tc.maxRank); got != tc.want {
				t.Errorf("ftsRelevance(%d, %d) = %v, want %v", tc.rank, tc.maxRank, got, tc.want)
			}
		})
	}
}

func TestKeywordOnlyResults(t *testing.T) {
	t.Run("rank zero keeps its similarity untouched", func(t *testing.T) {
		in := []SearchResult{{Drawer: Drawer{ID: "a"}, Rank: 0, Similarity: 0.25}}
		got := keywordOnlyResults(in, 5)
		if got[0].Similarity != 0.25 {
			t.Errorf("Similarity = %v, want the original 0.25 — rank 0 means semantic-only", got[0].Similarity)
		}
	})

	t.Run("ranked rows are rescored", func(t *testing.T) {
		in := []SearchResult{{Drawer: Drawer{ID: "a"}, Rank: -2}, {Drawer: Drawer{ID: "b"}, Rank: -1}}
		got := keywordOnlyResults(in, 5)
		if got[0].Similarity <= got[1].Similarity {
			t.Errorf("rank -2 scored %v, rank -1 scored %v — more negative rank must score higher",
				got[0].Similarity, got[1].Similarity)
		}
		if got[0].Similarity < 0.5 {
			t.Errorf("Similarity = %v, want >= 0.5 — the scale starts at the midpoint", got[0].Similarity)
		}
	})

	t.Run("trims to the limit", func(t *testing.T) {
		in := []SearchResult{{Rank: -3}, {Rank: -2}, {Rank: -1}}
		if got := keywordOnlyResults(in, 2); len(got) != 2 {
			t.Errorf("len = %d, want 2", len(got))
		}
	})

	t.Run("fewer results than the limit are returned whole", func(t *testing.T) {
		in := []SearchResult{{Rank: -1}}
		if got := keywordOnlyResults(in, 10); len(got) != 1 {
			t.Errorf("len = %d, want 1", len(got))
		}
	})
}

// --- drawer_service: semantic/keyword merge --------------------------------

func TestResolveRanked(t *testing.T) {
	ds := NewDrawerService(newTestStore(t), nil, DefaultConfig())
	ctx := context.Background()
	kept := addDrawer(t, ds, "w", "r", "a drawer that exists in the store")

	got := ds.resolveRanked(ctx, []ScoredResult{
		{DrawerID: kept.ID, Similarity: 0.9},
		{DrawerID: "not-in-the-store", Similarity: 0.8},
	})

	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 — a ranked ID with no drawer must be dropped, not fail the search", len(got))
	}
	res, ok := got[kept.ID]
	if !ok {
		t.Fatalf("resolved map is missing %q", kept.ID)
	}
	if res.Similarity != 0.9 {
		t.Errorf("Similarity = %v, want the ranked score 0.9", res.Similarity)
	}
	if res.Drawer.ID != kept.ID {
		t.Errorf("Drawer.ID = %q, want %q", res.Drawer.ID, kept.ID)
	}
}

func TestMergeSearchResults(t *testing.T) {
	semantic := map[string]SearchResult{
		"s1": {Drawer: Drawer{ID: "s1"}, Similarity: 0.9},
		"s2": {Drawer: Drawer{ID: "s2"}, Similarity: 0.7},
	}

	t.Run("semantic results come first in ranked order", func(t *testing.T) {
		ranked := []ScoredResult{{DrawerID: "s1"}, {DrawerID: "s2"}}
		got := mergeSearchResults(ranked, semantic, nil, 10)
		if len(got) != 2 || got[0].Drawer.ID != "s1" || got[1].Drawer.ID != "s2" {
			t.Fatalf("got %v, want [s1 s2] in ranked order", ids(got))
		}
	})

	t.Run("ranked IDs missing from the semantic map are dropped", func(t *testing.T) {
		ranked := []ScoredResult{{DrawerID: "s1"}, {DrawerID: "gone"}, {DrawerID: "s2"}}
		got := mergeSearchResults(ranked, semantic, nil, 10)
		if len(got) != 2 {
			t.Fatalf("got %v, want only the two resolvable IDs", ids(got))
		}
	})

	t.Run("FTS5 hits already in the semantic set are not duplicated", func(t *testing.T) {
		ranked := []ScoredResult{{DrawerID: "s1"}}
		fts := []SearchResult{{Drawer: Drawer{ID: "s1"}, Rank: -1}}
		got := mergeSearchResults(ranked, semantic, fts, 10)
		if len(got) != 1 {
			t.Fatalf("got %v, want a single s1 — the FTS5 hit duplicates the semantic one", ids(got))
		}
		if got[0].Similarity != 0.9 {
			t.Errorf("Similarity = %v, want the semantic 0.9 to win over the FTS5 score", got[0].Similarity)
		}
	})

	t.Run("FTS5-only hits are appended with a normalized score", func(t *testing.T) {
		ranked := []ScoredResult{{DrawerID: "s1"}}
		fts := []SearchResult{{Drawer: Drawer{ID: "k1"}, Rank: -4}}
		got := mergeSearchResults(ranked, semantic, fts, 10)
		if len(got) != 2 || got[1].Drawer.ID != "k1" {
			t.Fatalf("got %v, want [s1 k1]", ids(got))
		}
		if got[1].Similarity < 0.5 {
			t.Errorf("Similarity = %v, want >= 0.5", got[1].Similarity)
		}
		if got[1].Rank != -4 {
			t.Errorf("Rank = %d, want the original -4 preserved", got[1].Rank)
		}
	})

	t.Run("duplicate FTS5 IDs are added once", func(t *testing.T) {
		fts := []SearchResult{
			{Drawer: Drawer{ID: "k1"}, Rank: -2},
			{Drawer: Drawer{ID: "k1"}, Rank: -1},
		}
		got := mergeSearchResults(nil, semantic, fts, 10)
		if len(got) != 1 {
			t.Fatalf("got %v, want a single k1", ids(got))
		}
	})

	t.Run("trims to the limit", func(t *testing.T) {
		ranked := []ScoredResult{{DrawerID: "s1"}, {DrawerID: "s2"}}
		fts := []SearchResult{{Drawer: Drawer{ID: "k1"}, Rank: -1}}
		got := mergeSearchResults(ranked, semantic, fts, 2)
		if len(got) != 2 {
			t.Fatalf("got %v, want 2 after trimming", ids(got))
		}
	})
}

func ids(results []SearchResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Drawer.ID)
	}
	return out
}

// --- miner_convo -----------------------------------------------------------

func TestResolveMineConfig(t *testing.T) {
	t.Run("nil config with no yaml defaults the wing to the directory name", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "my-project")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		got := resolveMineConfig(dir, nil)
		if got.Wing != "my-project" {
			t.Errorf("Wing = %q, want %q", got.Wing, "my-project")
		}
	})

	t.Run("nil config loads mempalace.yaml when present", func(t *testing.T) {
		dir := t.TempDir()
		writeTestFile(t, filepath.Join(dir, "mempalace.yaml"), "wing: from-yaml\n")
		got := resolveMineConfig(dir, nil)
		if got.Wing != "from-yaml" {
			t.Errorf("Wing = %q, want %q", got.Wing, "from-yaml")
		}
	})

	t.Run("an empty wing in a loaded config still defaults", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "fallback-dir")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		writeTestFile(t, filepath.Join(dir, "mempalace.yaml"), "rooms: []\n")
		got := resolveMineConfig(dir, nil)
		if got.Wing != "fallback-dir" {
			t.Errorf("Wing = %q, want %q", got.Wing, "fallback-dir")
		}
	})

	t.Run("an explicit wing is preserved", func(t *testing.T) {
		got := resolveMineConfig(t.TempDir(), &MineConfig{Wing: "explicit"})
		if got.Wing != "explicit" {
			t.Errorf("Wing = %q, want %q", got.Wing, "explicit")
		}
	})

	t.Run("an explicit config with an empty wing takes the directory name", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "named")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		got := resolveMineConfig(dir, &MineConfig{})
		if got.Wing != "named" {
			t.Errorf("Wing = %q, want %q", got.Wing, "named")
		}
	})
}

// fakeDirEntry is the minimum os.DirEntry needed to drive visitMineDir.
type fakeDirEntry struct{ name string }

func (f fakeDirEntry) Name() string             { return f.name }
func (fakeDirEntry) IsDir() bool                { return true }
func (fakeDirEntry) Type() fs.FileMode          { return fs.ModeDir }
func (fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

func TestVisitMineDir(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		skip bool
	}{
		{"dotfile directory is skipped", ".git", true},
		{"a bare dot is the walk root, not a dotfile", ".", false},
		{"named skip directory", "node_modules", true},
		{"ordinary directory is descended", "internal", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := visitMineDir(fakeDirEntry{name: tc.dir})
			if tc.skip && !errors.Is(got, filepath.SkipDir) {
				t.Errorf("visitMineDir(%q) = %v, want SkipDir", tc.dir, got)
			}
			if !tc.skip && got != nil {
				t.Errorf("visitMineDir(%q) = %v, want nil", tc.dir, got)
			}
		})
	}
}

func TestConvoMinerParseFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "ok.jsonl"), `{"role":"user","content":"hi"}
{"role":"assistant","content":"hello"}
`)
	writeTestFile(t, filepath.Join(dir, "ok.txt"), "> question\nAssistant: answer\n")
	writeTestFile(t, filepath.Join(dir, "ok.md"), "> question\nAssistant: answer\n")
	writeTestFile(t, filepath.Join(dir, "skip.go"), "package main\n")
	writeTestFile(t, filepath.Join(dir, "empty.txt"), "no markers here\n")
	// A directory named like a conversation file: opening it succeeds but
	// reading it fails, which is the parse-error branch.
	for _, bad := range []string{"bad.jsonl", "bad.txt"} {
		if err := os.MkdirAll(filepath.Join(dir, bad), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}

	tests := []struct {
		name          string
		file          string
		wantParsed    bool
		wantExchanges int
		wantErrors    int
	}{
		{"jsonl parses", "ok.jsonl", true, 1, 0},
		{"txt parses", "ok.txt", true, 1, 0},
		{"md parses", "ok.md", true, 1, 0},
		{"text with no markers parses to zero exchanges", "empty.txt", true, 0, 0},
		{"unsupported extension is skipped without an error", "skip.go", false, 0, 0},
		{"unreadable jsonl counts an error", "bad.jsonl", false, 0, 1},
		{"unreadable text counts an error", "bad.txt", false, 0, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &convoMiner{result: &MineResult{}}
			got, parsed := m.parseFile(filepath.Join(dir, tc.file), tc.file)
			if parsed != tc.wantParsed {
				t.Fatalf("parsed = %v, want %v", parsed, tc.wantParsed)
			}
			if len(got) != tc.wantExchanges {
				t.Errorf("exchanges = %d, want %d", len(got), tc.wantExchanges)
			}
			if m.result.Errors != tc.wantErrors {
				t.Errorf("result.Errors = %d, want %d", m.result.Errors, tc.wantErrors)
			}
		})
	}
}

func TestConvoMinerAddExchanges(t *testing.T) {
	newMiner := func(t *testing.T, cfg *MineConfig) *convoMiner {
		t.Helper()
		return &convoMiner{
			ctx:    context.Background(),
			palace: newTestPalace(t),
			cfg:    cfg,
			result: &MineResult{},
		}
	}

	t.Run("counts additions and reports progress", func(t *testing.T) {
		var gotFile string
		var gotAdded, gotSkipped, gotErrors int
		m := newMiner(t, &MineConfig{
			Wing: "w",
			Progress: func(file string, added, skipped, errors int) {
				gotFile, gotAdded, gotSkipped, gotErrors = file, added, skipped, errors
			},
		})

		m.addExchanges("chat.txt", []exchange{{content: "first"}, {content: "second"}})

		if m.result.Added != 2 || m.result.Processed != 2 {
			t.Errorf("Added=%d Processed=%d, want 2 and 2", m.result.Added, m.result.Processed)
		}
		if gotFile != "chat.txt" || gotAdded != 2 || gotSkipped != 0 || gotErrors != 0 {
			t.Errorf("progress = (%q, %d, %d, %d), want (chat.txt, 2, 0, 0)",
				gotFile, gotAdded, gotSkipped, gotErrors)
		}
	})

	t.Run("duplicate content is skipped, not counted as an error", func(t *testing.T) {
		var gotAdded, gotSkipped int
		m := newMiner(t, &MineConfig{
			Wing: "w",
			Progress: func(_ string, added, skipped, _ int) {
				gotAdded, gotSkipped = added, skipped
			},
		})

		// Identical content in two different chunk slots of the same file:
		// the ids differ, so the content-hash dedup fires on the second.
		m.addExchanges("chat.txt", []exchange{{content: "same text"}, {content: "same text"}})

		if m.result.Added != 1 || m.result.Skipped != 1 {
			t.Errorf("Added=%d Skipped=%d, want 1 and 1", m.result.Added, m.result.Skipped)
		}
		if m.result.Errors != 0 {
			t.Errorf("Errors = %d, want 0 — a duplicate is not an error", m.result.Errors)
		}
		if gotAdded != 1 || gotSkipped != 1 {
			t.Errorf("progress added=%d skipped=%d, want 1 and 1", gotAdded, gotSkipped)
		}
	})

	t.Run("a non-duplicate failure is counted as an error", func(t *testing.T) {
		var gotErrors int
		m := newMiner(t, &MineConfig{
			Wing: "w",
			Progress: func(_ string, _, _, errors int) {
				gotErrors = errors
			},
		})

		// Empty content is rejected by AddDrawer for a reason that is not a
		// duplicate, which is the other side of the error branch.
		m.addExchanges("chat.txt", []exchange{{content: ""}})

		if m.result.Errors != 1 || m.result.Skipped != 0 || m.result.Added != 0 {
			t.Errorf("Errors=%d Skipped=%d Added=%d, want 1, 0, 0",
				m.result.Errors, m.result.Skipped, m.result.Added)
		}
		if gotErrors != 1 {
			t.Errorf("progress errors = %d, want 1", gotErrors)
		}
	})

	t.Run("a nil progress callback is not invoked", func(t *testing.T) {
		m := newMiner(t, &MineConfig{Wing: "w"})
		m.addExchanges("chat.txt", []exchange{{content: "only one"}})
		if m.result.Added != 1 {
			t.Errorf("Added = %d, want 1", m.result.Added)
		}
	})

	t.Run("room detection uses the configured keywords", func(t *testing.T) {
		m := newMiner(t, &MineConfig{
			Wing:  "w",
			Rooms: []RoomDef{{Name: "auth", Keywords: []string{"token"}}},
		})
		m.addExchanges("chat.txt", []exchange{{content: "refresh token handling"}})

		drawers, err := m.palace.ListDrawers(m.ctx, DrawerFilter{Wing: "w"})
		if err != nil {
			t.Fatalf("ListDrawers: %v", err)
		}
		if len(drawers) != 1 || drawers[0].Room != "auth" {
			t.Fatalf("got %d drawers, first room %v, want 1 drawer in room auth", len(drawers), roomOf(drawers))
		}
	})
}

func roomOf(drawers []*Drawer) string {
	if len(drawers) == 0 {
		return ""
	}
	return drawers[0].Room
}

// TestMineConversations_ReportsProgressForEmptyParse pins the distinction the
// (exchanges, parsed) return carries: a file that parses to zero exchanges
// still reports progress, while an unsupported extension does not.
func TestMineConversations_ReportsProgressForEmptyParse(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "nomarkers.txt"), "just prose with no turn markers\n")
	writeTestFile(t, filepath.Join(dir, "code.go"), "package main\n")

	var reported []string
	cfg := &MineConfig{
		Wing: "w",
		Progress: func(file string, _, _, _ int) {
			reported = append(reported, file)
		},
	}

	if _, err := MineConversations(context.Background(), newTestPalace(t), dir, cfg); err != nil {
		t.Fatalf("MineConversations: %v", err)
	}

	if len(reported) != 1 || reported[0] != "nomarkers.txt" {
		t.Errorf("progress reported for %v, want only [nomarkers.txt]", reported)
	}
}

// TestMineConversations_CountsWalkErrors pins the walkErr branch of visit: an
// unreadable subdirectory is tallied and the walk continues, rather than
// abandoning the rest of the tree.
func TestMineConversations_CountsWalkErrors(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: permission bits do not make a directory unreadable")
	}
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "chat.txt"), "> question\nAssistant: answer\n")

	locked := filepath.Join(dir, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	result, err := MineConversations(context.Background(), newTestPalace(t), dir, &MineConfig{Wing: "w"})
	if err != nil {
		t.Fatalf("MineConversations: %v", err)
	}
	if result.Errors == 0 {
		t.Error("Errors = 0, want the unreadable directory to be counted")
	}
	if result.Added != 1 {
		t.Errorf("Added = %d, want 1 — the readable file must still be mined", result.Added)
	}
}

// --- tool_kg_extract -------------------------------------------------------

func TestTripleSinkAdd(t *testing.T) {
	t.Run("blank fields are dropped", func(t *testing.T) {
		ts := newTripleSink()
		ts.add("", "imports", "x", "r", 0.9)
		ts.add("a", "", "x", "r", 0.9)
		ts.add("a", "imports", "", "r", 0.9)
		ts.add("   ", "imports", "x", "r", 0.9)
		if len(ts.out) != 0 {
			t.Errorf("got %d triples, want 0", len(ts.out))
		}
	})

	t.Run("a self-referential triple is dropped case-insensitively", func(t *testing.T) {
		ts := newTripleSink()
		ts.add("Handler", "defined_in", "handler", "r", 0.9)
		if len(ts.out) != 0 {
			t.Errorf("got %d triples, want 0 — subject and object are the same entity", len(ts.out))
		}
	})

	t.Run("duplicates are dropped case-insensitively, first reason wins", func(t *testing.T) {
		ts := newTripleSink()
		ts.add("A", "defined_in", "f.go", "first", 0.95)
		ts.add("a", "DEFINED_IN", "F.GO", "second", 0.10)
		if len(ts.out) != 1 {
			t.Fatalf("got %d triples, want 1", len(ts.out))
		}
		if ts.out[0].Reason != "first" {
			t.Errorf("Reason = %q, want %q", ts.out[0].Reason, "first")
		}
		if ts.out[0].Confidence != 0.95 {
			t.Errorf("Confidence = %v, want 0.95", ts.out[0].Confidence)
		}
	})

	t.Run("fields are trimmed and the predicate normalized", func(t *testing.T) {
		ts := newTripleSink()
		ts.add("  Sub  ", "  part of  ", "  Obj  ", "r", 0.7)
		if len(ts.out) != 1 {
			t.Fatalf("got %d triples, want 1", len(ts.out))
		}
		got := ts.out[0]
		if got.Subject != "Sub" || got.Object != "Obj" {
			t.Errorf("Subject=%q Object=%q, want Sub and Obj", got.Subject, got.Object)
		}
		if got.Predicate != "part_of" {
			t.Errorf("Predicate = %q, want %q", got.Predicate, "part_of")
		}
	})
}

func TestImportBlockBody(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		start int
		want  string
	}{
		{"terminated block stops at the closing paren", "import (\n\t\"a\"\n)\nrest", len("import ("), "\n\t\"a\""},
		{"unterminated block runs to the end", "import (\n\t\"a\"\n\t\"b\"", len("import ("), "\n\t\"a\"\n\t\"b\""},
		{"immediately closed block is empty", "import (\n)", len("import ("), ""},
		{"an indented closing paren does not terminate", "import (\n\t\"a\"\n\t)", len("import ("), "\n\t\"a\"\n\t)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := importBlockBody(tc.text, tc.start); got != tc.want {
				t.Errorf("importBlockBody = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEmitImport(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		sourceFile string
		wantSubj   string
		wantObj    string
		wantConf   float64
		wantEmit   bool
	}{
		{name: "empty path emits nothing", path: ""},
		{name: "stdlib path emits nothing", path: "strings"},
		{name: "relative path emits nothing", path: "./local"},
		{
			name: "third-party path with a source file anchors to the file",
			path: "github.com/x/y", sourceFile: "internal/a/b.go",
			wantSubj: "b.go", wantObj: "github.com/x/y", wantConf: 0.95, wantEmit: true,
		},
		{
			name:     "third-party path with no source file anchors to the codebase",
			path:     "github.com/x/y",
			wantSubj: "codebase", wantObj: "github.com/x/y", wantConf: 0.50, wantEmit: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := newTripleSink()
			emitImport("", tc.path, tc.sourceFile, ts.add)
			if !tc.wantEmit {
				if len(ts.out) != 0 {
					t.Fatalf("got %d triples, want 0", len(ts.out))
				}
				return
			}
			if len(ts.out) != 1 {
				t.Fatalf("got %d triples, want 1", len(ts.out))
			}
			got := ts.out[0]
			if got.Subject != tc.wantSubj || got.Object != tc.wantObj || got.Predicate != "imports" {
				t.Errorf("triple = (%q, %q, %q), want (%q, imports, %q)",
					got.Subject, got.Predicate, got.Object, tc.wantSubj, tc.wantObj)
			}
			if got.Confidence != tc.wantConf {
				t.Errorf("Confidence = %v, want %v", got.Confidence, tc.wantConf)
			}
		})
	}
}

func TestExtractImportTriples(t *testing.T) {
	t.Run("single-line forms", func(t *testing.T) {
		text := `import "github.com/a/one"
from "github.com/b/two"
require("github.com/c/three")
import alias "github.com/d/four"
`
		ts := newTripleSink()
		extractImportTriples(text, "", ts.add)
		want := []string{"github.com/a/one", "github.com/b/two", "github.com/c/three", "github.com/d/four"}
		assertObjects(t, ts.out, want)
	})

	t.Run("parenthesized Go block", func(t *testing.T) {
		text := "package x\n\nimport (\n\t\"fmt\"\n\t\"github.com/a/one\"\n\t\"github.com/b/two\"\n)\n\nfunc noop() {}\n"
		ts := newTripleSink()
		extractImportTriples(text, "", ts.add)
		// "fmt" is stdlib and filtered out.
		assertObjects(t, ts.out, []string{"github.com/a/one", "github.com/b/two"})
	})

	t.Run("a path seen on a single line is not re-emitted from the block", func(t *testing.T) {
		text := "import \"github.com/a/one\"\n\nimport (\n\t\"github.com/a/one\"\n\t\"github.com/b/two\"\n)\n"
		ts := newTripleSink()
		extractImportTriples(text, "", ts.add)
		assertObjects(t, ts.out, []string{"github.com/a/one", "github.com/b/two"})
	})

	t.Run("quoted strings outside an import block are ignored", func(t *testing.T) {
		text := "func f() { log(\"github.com/not/an/import\") }\n"
		ts := newTripleSink()
		extractImportTriples(text, "", ts.add)
		assertObjects(t, ts.out, nil)
	})
}

func TestExtractDeclarationTriples(t *testing.T) {
	const decls = "func Handler() {}\n" +
		"func (s *Server) Serve() {}\n" +
		"type Config struct {\n}\n" +
		"type Store interface {\n}\n" +
		"def python_thing():\n" +
		"class JSThing:\n" +
		"const result = 1\n"

	t.Run("nothing is emitted without a source file", func(t *testing.T) {
		ts := newTripleSink()
		extractDeclarationTriples(decls, "", ts.add)
		if len(ts.out) != 0 {
			t.Errorf("got %d triples, want 0 — there is no file to anchor them to", len(ts.out))
		}
	})

	t.Run("every declaration kind is anchored to the source file", func(t *testing.T) {
		ts := newTripleSink()
		extractDeclarationTriples(decls, "internal/x/y.go", ts.add)

		subjects := map[string]ExtractedTriple{}
		for _, tr := range ts.out {
			subjects[tr.Subject] = tr
		}
		for _, want := range []string{"Handler", "Serve", "Config", "Store", "python_thing", "JSThing"} {
			tr, ok := subjects[want]
			if !ok {
				t.Errorf("missing a triple for %q; got %v", want, subjectsOf(ts.out))
				continue
			}
			if tr.Predicate != "defined_in" || tr.Object != "internal/x/y.go" || tr.Confidence != 0.95 {
				t.Errorf("%q = (%q, %q, %v), want (defined_in, internal/x/y.go, 0.95)",
					want, tr.Predicate, tr.Object, tr.Confidence)
			}
		}
		if _, ok := subjects["result"]; ok {
			t.Error("`const result` produced a triple; common words must be filtered from the generic pattern")
		}
	})

	t.Run("the Go pattern is not subject to the common-word filter", func(t *testing.T) {
		// `func Result` is capitalized, so goFuncDeclRe matches it. The
		// common-word filter applies only to the generic pattern, so this
		// must survive even though "result" is a common word.
		ts := newTripleSink()
		extractDeclarationTriples("func Result() {}\n", "f.go", ts.add)
		assertSubjects(t, ts.out, []string{"Result"})
	})

	t.Run("lowercase Go declarations are not matched", func(t *testing.T) {
		ts := newTripleSink()
		extractDeclarationTriples("func unexported() {}\ntype hidden struct {\n}\n", "f.go", ts.add)
		assertSubjects(t, ts.out, nil)
	})

	t.Run("heuristic order decides the reason on a collision", func(t *testing.T) {
		// A single name reachable by both the Go func pattern and the
		// generic one: the sink dedupes, so the earlier heuristic's reason
		// is the one recorded.
		ts := newTripleSink()
		extractDeclarationTriples("func Thing() {}\nclass Thing:\n", "f.go", ts.add)
		if len(ts.out) != 1 {
			t.Fatalf("got %d triples, want 1", len(ts.out))
		}
		if ts.out[0].Reason != "Go func declaration in source file" {
			t.Errorf("Reason = %q, want the Go func reason to win", ts.out[0].Reason)
		}
	})
}

func TestExtractPathTriples(t *testing.T) {
	const text = "see internal/x/sibling.go and internal/other/far.go and internal/x/self.go\n"

	t.Run("nothing is emitted without a source file", func(t *testing.T) {
		ts := newTripleSink()
		extractPathTriples(text, "", ts.add)
		if len(ts.out) != 0 {
			t.Errorf("got %d triples, want 0", len(ts.out))
		}
	})

	t.Run("only same-directory paths become part_of", func(t *testing.T) {
		ts := newTripleSink()
		extractPathTriples(text, "internal/x/self.go", ts.add)
		assertSubjects(t, ts.out, []string{"internal/x/sibling.go"})
		if len(ts.out) == 1 {
			got := ts.out[0]
			if got.Predicate != "part_of" || got.Object != "self.go" || got.Confidence != 0.70 {
				t.Errorf("triple = (%q, %q, %v), want (part_of, self.go, 0.7)",
					got.Predicate, got.Object, got.Confidence)
			}
		}
	})

	t.Run("a source file at the repo root has no directory to share", func(t *testing.T) {
		ts := newTripleSink()
		extractPathTriples("see internal/x/a.go\n", "main.go", ts.add)
		assertSubjects(t, ts.out, nil)
	})
}

// TestExtractTriples_EndToEnd runs the three extraction steps together over
// one chunk, which is what extractTriples did inline before the split.
func TestExtractTriples_EndToEnd(t *testing.T) {
	text := "package handler\n\n" +
		"import (\n\t\"fmt\"\n\t\"github.com/dimetron/pi-go/internal/palace\"\n)\n\n" +
		"type Handler struct {\n}\n\n" +
		"func Serve() {}\n\n" +
		"// see internal/api/routes.go for the table\n"

	t.Run("with a source file", func(t *testing.T) {
		got := extractTriples(text, "internal/api/handler.go")
		want := map[string]string{
			"github.com/dimetron/pi-go/internal/palace": "imports",
			"Handler":                "defined_in",
			"Serve":                  "defined_in",
			"internal/api/routes.go": "part_of",
		}
		byKey := map[string]ExtractedTriple{}
		for _, tr := range got {
			// Imports are keyed by object, declarations and paths by subject.
			if tr.Predicate == "imports" {
				byKey[tr.Object] = tr
			} else {
				byKey[tr.Subject] = tr
			}
		}
		for key, pred := range want {
			tr, ok := byKey[key]
			if !ok {
				t.Errorf("missing a triple for %q", key)
				continue
			}
			if tr.Predicate != pred {
				t.Errorf("%q predicate = %q, want %q", key, tr.Predicate, pred)
			}
		}
		for _, tr := range got {
			if tr.Object == "fmt" {
				t.Error("stdlib import fmt leaked into the candidates")
			}
		}
	})

	t.Run("without a source file only imports survive", func(t *testing.T) {
		got := extractTriples(text, "")
		for _, tr := range got {
			if tr.Predicate != "imports" {
				t.Errorf("unexpected %q triple with no source file: %+v", tr.Predicate, tr)
			}
			if tr.Subject != "codebase" {
				t.Errorf("Subject = %q, want %q when there is no file to anchor to", tr.Subject, "codebase")
			}
		}
		if len(got) == 0 {
			t.Error("expected at least the third-party import to be extracted")
		}
	})

	t.Run("prose yields nothing", func(t *testing.T) {
		if got := extractTriples("Just a sentence about nothing in particular.", "notes.md"); len(got) != 0 {
			t.Errorf("got %d triples from prose, want 0: %v", len(got), got)
		}
	})
}

func subjectsOf(triples []ExtractedTriple) []string {
	out := make([]string, 0, len(triples))
	for _, tr := range triples {
		out = append(out, tr.Subject)
	}
	return out
}

func objectsOf(triples []ExtractedTriple) []string {
	out := make([]string, 0, len(triples))
	for _, tr := range triples {
		out = append(out, tr.Object)
	}
	return out
}

func assertSubjects(t *testing.T, triples []ExtractedTriple, want []string) {
	t.Helper()
	assertSetEqual(t, "subjects", subjectsOf(triples), want)
}

func assertObjects(t *testing.T, triples []ExtractedTriple, want []string) {
	t.Helper()
	assertSetEqual(t, "objects", objectsOf(triples), want)
}

func assertSetEqual(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = [%s], want [%s]", label, strings.Join(got, " "), strings.Join(want, " "))
	}
	gotSet := make(map[string]bool, len(got))
	for _, g := range got {
		gotSet[g] = true
	}
	for _, w := range want {
		if !gotSet[w] {
			t.Fatalf("%s = [%s], want [%s]", label, strings.Join(got, " "), strings.Join(want, " "))
		}
	}
}
