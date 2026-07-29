package palace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMineConfigFor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	t.Run("nil config falls back to the directory basename", func(t *testing.T) {
		t.Parallel()
		got := mineConfigFor(dir, nil)
		if got == nil {
			t.Fatal("mineConfigFor returned nil")
		}
		if got.Wing != filepath.Base(dir) {
			t.Errorf("Wing = %q, want %q", got.Wing, filepath.Base(dir))
		}
	})

	t.Run("empty wing is filled in", func(t *testing.T) {
		t.Parallel()
		got := mineConfigFor(dir, &MineConfig{})
		if got.Wing != filepath.Base(dir) {
			t.Errorf("Wing = %q, want %q", got.Wing, filepath.Base(dir))
		}
	})

	t.Run("explicit wing is preserved", func(t *testing.T) {
		t.Parallel()
		got := mineConfigFor(dir, &MineConfig{Wing: "custom"})
		if got.Wing != "custom" {
			t.Errorf("Wing = %q, want %q", got.Wing, "custom")
		}
	})
}

func TestIsMineableFile(t *testing.T) {
	dir := t.TempDir()

	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
		return path
	}

	goFile := write("main.go", "package main\n")
	noExt := write("LICENSE", "MIT\n")
	unsupported := write("thing.bin", "data\n")
	binary := write("blob.json", "{\"a\":\"\x00\x01\x02binary\"}")
	big := write("huge.go", strings.Repeat("x", 513*1024))

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading temp dir: %v", err)
	}
	byName := map[string]os.DirEntry{}
	for _, e := range entries {
		byName[e.Name()] = e
	}

	tests := []struct {
		name    string
		path    string
		relPath string
		want    bool
	}{
		{"supported source file", goFile, "main.go", true},
		{"no extension is skipped", noExt, "LICENSE", false},
		{"unsupported extension is skipped", unsupported, "thing.bin", false},
		{"binary content is skipped", binary, "blob.json", false},
		{"oversized file is skipped", big, "huge.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := byName[filepath.Base(tt.path)]
			if d == nil {
				t.Fatalf("no dir entry for %s", tt.path)
			}
			if got := isMineableFile(tt.path, tt.relPath, d, nil, nil); got != tt.want {
				t.Errorf("isMineableFile(%s) = %v, want %v", tt.relPath, got, tt.want)
			}
		})
	}
}

func TestIsMineableFileRespectsGitignore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.go")
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading temp dir: %v", err)
	}
	d := entries[0]

	// Via the git-reported ignore set.
	if isMineableFile(path, "secret.go", d, map[string]bool{"secret.go": true}, nil) {
		t.Error("a git-ignored file should not be mined")
	}
	// Via manually parsed patterns, used when the dir is not a repo.
	if isMineableFile(path, "secret.go", d, nil, []string{"*.go"}) {
		t.Error("a gitignore-pattern match should not be mined")
	}
	// Sanity: with neither, it is mineable.
	if !isMineableFile(path, "secret.go", d, nil, nil) {
		t.Error("an unignored source file should be mined")
	}
}

func TestCollectChunks(t *testing.T) {
	t.Parallel()
	tasks := []fileTask{
		{relPath: "a.go", room: "code", chunks: []string{"one", "two"}},
		{relPath: "b.go", room: "code", chunks: []string{"three"}},
	}
	cfg := &MineConfig{Wing: "w"}
	result := &MineResult{}

	got := collectChunks(tasks, cfg, result)

	if len(got) != 3 {
		t.Fatalf("collectChunks returned %d chunks, want 3", len(got))
	}
	if result.Processed != 3 {
		t.Errorf("result.Processed = %d, want 3", result.Processed)
	}
	// Chunk indexes restart per file.
	if got[0].chunkIdx != 0 || got[1].chunkIdx != 1 || got[2].chunkIdx != 0 {
		t.Errorf("chunk indexes = %d/%d/%d, want 0/1/0", got[0].chunkIdx, got[1].chunkIdx, got[2].chunkIdx)
	}
	for _, c := range got {
		if c.wing != "w" {
			t.Errorf("chunk wing = %q, want %q", c.wing, "w")
		}
	}
}

func TestCollectChunksReportsProgress(t *testing.T) {
	t.Parallel()
	var calls int
	cfg := &MineConfig{
		Wing:     "w",
		Progress: func(string, int, int, int) { calls++ },
	}
	tasks := []fileTask{{relPath: "a.go", chunks: []string{"x"}}}

	collectChunks(tasks, cfg, &MineResult{})
	if calls == 0 {
		t.Error("expected the progress callback to be invoked")
	}
}

func TestCollectChunksEmpty(t *testing.T) {
	t.Parallel()
	result := &MineResult{}
	if got := collectChunks(nil, &MineConfig{Wing: "w"}, result); len(got) != 0 {
		t.Errorf("expected no chunks, got %d", len(got))
	}
	if result.Processed != 0 {
		t.Errorf("result.Processed = %d, want 0", result.Processed)
	}
}

func TestBuildDrawers(t *testing.T) {
	t.Parallel()
	chunks := []chunkJob{
		{wing: "w", room: "r", relPath: "a.go", chunkIdx: 0, content: "alpha"},
		{wing: "w", room: "r", relPath: "a.go", chunkIdx: 1, content: "beta"},
	}
	embeddings := [][]float32{{1, 2}, nil}
	result := &MineResult{}

	drawers := buildDrawers(chunks, embeddings, result)

	if len(drawers) != 2 {
		t.Fatalf("buildDrawers returned %d drawers, want 2", len(drawers))
	}
	if drawers[0].Embedding == nil {
		t.Error("first drawer should carry its embedding")
	}
	// A failed embedding batch leaves a nil, and the drawer is stored without
	// a vector rather than with someone else's.
	if drawers[1].Embedding != nil {
		t.Error("second drawer should have no embedding")
	}
	for i, d := range drawers {
		if d.ContentHash != HashContent(chunks[i].content) {
			t.Errorf("drawer %d content hash does not match its content", i)
		}
		if d.SourceFile != "a.go" || d.AddedBy != "miner:project" {
			t.Errorf("drawer %d = %+v, unexpected provenance", i, d)
		}
	}
}

func TestBuildDrawersSkipsDuplicateIDs(t *testing.T) {
	t.Parallel()
	// Identical chunks produce identical IDs; only the first is kept.
	dup := chunkJob{wing: "w", room: "r", relPath: "a.go", chunkIdx: 0, content: "same"}
	result := &MineResult{}

	drawers := buildDrawers([]chunkJob{dup, dup}, nil, result)

	if len(drawers) != 1 {
		t.Errorf("expected duplicates to collapse to 1 drawer, got %d", len(drawers))
	}
	if result.Skipped != 1 {
		t.Errorf("result.Skipped = %d, want 1", result.Skipped)
	}
}

func TestBuildDrawersWithoutEmbeddings(t *testing.T) {
	t.Parallel()
	chunks := []chunkJob{{wing: "w", room: "r", relPath: "a.go", content: "x"}}

	drawers := buildDrawers(chunks, nil, &MineResult{})
	if len(drawers) != 1 {
		t.Fatalf("expected 1 drawer, got %d", len(drawers))
	}
	if drawers[0].Embedding != nil {
		t.Error("expected no embedding when none were computed")
	}
}

func TestEmbedWorkers(t *testing.T) {
	t.Parallel()
	if got := embedWorkers(""); got != 1 {
		t.Errorf("embedWorkers(\"\") = %d, want 1 — no model means no extra workers", got)
	}
	got := embedWorkers("/some/model/path")
	if got < 1 || got > maxEmbedSessions {
		t.Errorf("embedWorkers() = %d, outside [1, %d]", got, maxEmbedSessions)
	}
}
