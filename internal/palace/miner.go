package palace

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// defaultChunkSize is the chunk size for project mining, in characters.
//
// It must not exceed what the embedder can actually consume. Embed() truncates
// every input to maxCharLength (512 chars ≈ the model's 128-token cap), so the
// previous 1500-char chunks were silently cut down before embedding: measured
// across this repo, 98% of chunks were truncated and 59.7% of all mined text
// never reached the model at all. The drawer kept the full 1500 chars, but its
// vector only described the first third — so most indexed content was invisible
// to semantic search while appearing to be indexed.
//
// 512 is the largest value that still fits: probing the real tokenizer, a
// 512-char chunk of dense Go source still influences its own embedding (cosine
// of the full chunk against its first half is 0.80, not ~1.0), which it would
// not if the tail had been dropped.
//
// This produces roughly 3x more chunks than before. That cost is real but it is
// the price of the index actually covering the corpus; it is offset by the fp32
// model switch (3.1x faster embedding) and by hash-based incremental mining,
// which skips unchanged chunks entirely on re-runs.
const defaultChunkSize = 512

// defaultChunkOverlap keeps the same ~13% overlap ratio the 1500/200 pair had.
const defaultChunkOverlap = 64

// ProgressFunc is called after each file is processed during mining.
// file is the relative path, added/skipped/errors are counts for that file.
type ProgressFunc func(file string, added, skipped, errors int)

// PhaseFunc reports progress *inside* a long phase, before the work is done.
//
// Embedding dominates a mining run and operates on chunks rather than files, so
// the old per-file callback went silent for minutes at a time and the run looked
// hung. PhaseFunc is called with the item about to be processed — the file the
// next chunk came from — so a slow phase shows where it actually is.
type PhaseFunc func(stage, item string, done, total int)

// MineConfig is the per-project configuration loaded from mempalace.yaml.
type MineConfig struct {
	Wing     string       `yaml:"wing"`
	Rooms    []RoomDef    `yaml:"rooms"`
	Progress ProgressFunc `yaml:"-"`
	Phase    PhaseFunc    `yaml:"-"`
}

// RoomDef defines a room with glob patterns and optional keywords for
// content-based room detection.
type RoomDef struct {
	Name     string   `yaml:"name"`
	Patterns []string `yaml:"patterns,omitempty"`
	Keywords []string `yaml:"keywords,omitempty"`
}

// MineResult tracks the outcome of a mining operation.
type MineResult struct {
	Added     int
	Skipped   int // duplicates
	Processed int
	Errors    int
}

// binarySniffBytes is how much of a file is inspected to decide if it is binary.
const binarySniffBytes = 8192

// isBinaryFile reports whether path looks like binary content.
//
// A NUL byte cannot occur in valid UTF-8 text, so its presence in the first few
// KB is the same heuristic git uses. Extension alone is not enough: a .json or
// .txt can hold a minified blob or an embedded payload, and embedding that is
// pure waste — it produces a meaningless vector at the same CPU cost as real
// source. Unreadable files are treated as binary so they are skipped rather than
// failing later.
func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()

	buf := make([]byte, binarySniffBytes)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return true
	}
	return bytes.IndexByte(buf[:n], 0) >= 0
}

// supportedExtensions lists file extensions eligible for project mining.
var supportedExtensions = map[string]bool{
	".go":    true,
	".py":    true,
	".js":    true,
	".ts":    true,
	".tsx":   true,
	".jsx":   true,
	".rs":    true,
	".java":  true,
	".kt":    true,
	".rb":    true,
	".c":     true,
	".h":     true,
	".cpp":   true,
	".hpp":   true,
	".cs":    true,
	".swift": true,
	".md":    true,
	".txt":   true,
	".yaml":  true,
	".yml":   true,
	".toml":  true,
	".json":  true,
	".sql":   true,
	".sh":    true,
	".bash":  true,
	".zsh":   true,
	".proto": true,
}

// skipDirNames are directory names always skipped during mining.
var skipDirNames = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	".git":         true,
	".pi-go":       true,
	"__pycache__":  true,
	"dist":         true,
	"build":        true,
	".idea":        true,
	".vscode":      true,
	".next":        true,
	".cache":       true,
	"target":       true,
	"bin":          true,
	".ralph":       true,
	".agents":      true,
}

// chunkText splits text into overlapping chunks, splitting on paragraph or
// line boundaries. Each chunk is at least minSize characters.
func chunkText(text string, size, overlap int) []string {
	if len(text) <= size {
		return []string{text}
	}

	var chunks []string
	start := 0
	for start < len(text) {
		end := start + size
		if end >= len(text) {
			end = len(text)
		} else {
			// Try to split on a paragraph boundary, but only if the
			// resulting chunk would be at least half the target size.
			if idx := strings.LastIndex(text[start:end], "\n\n"); idx > size/2 {
				end = start + idx + 2
			} else if idx := strings.LastIndex(text[start:end], "\n"); idx > size/2 {
				end = start + idx + 1
			}
		}

		chunk := strings.TrimSpace(text[start:end])
		if len(chunk) >= 50 {
			chunks = append(chunks, chunk)
		}

		if end >= len(text) {
			break
		}

		// Advance past the current chunk, applying overlap.
		next := end - overlap
		if next <= start {
			next = end // always make forward progress
		}
		start = next
	}

	return chunks
}

// detectRoom maps a file path to a room name using the configured rooms.
// It checks folder paths first, then filename, then keyword scoring,
// falling back to "general".
func detectRoom(filePath string, rooms []RoomDef) string {
	rel := filepath.ToSlash(filePath)

	// Check glob patterns.
	for _, room := range rooms {
		for _, pattern := range room.Patterns {
			matched, err := filepath.Match(pattern, rel)
			if err == nil && matched {
				return room.Name
			}
			// Also try matching against just the directory prefix.
			dir := filepath.Dir(rel)
			if matched, err := filepath.Match(pattern, dir+"/"); err == nil && matched {
				return room.Name
			}
		}
	}

	// Check if the file is inside a directory matching a room name.
	parts := strings.Split(rel, "/")
	for _, room := range rooms {
		for _, part := range parts {
			if strings.EqualFold(part, room.Name) {
				return room.Name
			}
		}
	}

	// First directory component as fallback.
	if len(parts) > 1 && parts[0] != "" {
		return parts[0]
	}

	return "general"
}

// readMempalaceYAML loads a MineConfig from a mempalace.yaml file.
func readMempalaceYAML(dir string) (*MineConfig, error) {
	path := filepath.Join(dir, "mempalace.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading mempalace.yaml: %w", err)
	}

	var cfg MineConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing mempalace.yaml: %w", err)
	}

	return &cfg, nil
}

// loadGitignore parses a .gitignore file and returns patterns as a slice.
func loadGitignore(dir string) []string {
	path := filepath.Join(dir, ".gitignore")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// isGitignored checks whether a relative path matches any of the gitignore patterns.
// This is a simplified check — not a full gitignore implementation.
func isGitignored(relPath string, patterns []string) bool {
	// .gitignore patterns are always slash-separated, so compare against a
	// slash-separated path. Splitting on filepath.Separator instead left every
	// pattern unmatched on Windows, where a backslash is also filepath.Match's
	// escape character.
	rel := filepath.ToSlash(relPath)
	for _, p := range patterns {
		p = strings.TrimSuffix(p, "/")
		// Check if any path component matches the pattern.
		for _, part := range strings.Split(rel, "/") {
			if matched, _ := filepath.Match(p, part); matched {
				return true
			}
		}
		// Also check the full relative path.
		if matched, _ := filepath.Match(p, rel); matched {
			return true
		}
	}
	return false
}

// GitIgnoredSet returns the set of files that git considers ignored in the
// given directory. It uses `git ls-files --others --ignored --exclude-standard`
// which respects all .gitignore files (nested, parent, and global), negation
// rules, and ** globs. Returns nil if git is not available or the directory is
// not a git repository.
func GitIgnoredSet(dir string) map[string]bool {
	return gitIgnoredSet(dir)
}

// IsGitIgnoredSet checks whether a relative path is in the ignored set.
func IsGitIgnoredSet(relPath string, ignored map[string]bool) bool {
	return isGitIgnoredSet(relPath, ignored)
}

// gitIgnoredSet returns the set of files that git considers ignored in the
// given directory. It uses `git ls-files --others --ignored --exclude-standard`
// which respects all .gitignore files (nested, parent, and global), negation
// rules, and ** globs. Returns nil if git is not available or the directory is
// not a git repository.
func gitIgnoredSet(dir string) map[string]bool {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil
	}

	// Resolve symlinks so that dir matches git's reported repo root.
	// On macOS, /var is a symlink to /private/var, and t.TempDir() returns
	// the symlink path while git reports the resolved path.
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realDir = dir
	}

	// Verify dir is inside a git repo.
	repoRoot, err := exec.Command(gitPath, "-C", realDir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil
	}
	root := strings.TrimSpace(string(repoRoot))

	// Get all ignored files relative to the repo root. --full-name is
	// required here: without it, `git -C realDir` reports paths relative to
	// realDir itself, and joining those against root below would produce
	// bogus paths (e.g. "../ignored.go") whenever realDir is a subdirectory
	// of the repo.
	cmd := exec.Command(gitPath, "-C", realDir, "ls-files", "--others", "--ignored", "--exclude-standard", "--full-name")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	ignored := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Convert to path relative to the mining directory.
		absPath := filepath.Join(root, line)
		rel, err := filepath.Rel(realDir, absPath)
		if err != nil {
			continue
		}
		ignored[filepath.ToSlash(rel)] = true
	}

	if len(ignored) == 0 {
		return nil
	}
	return ignored
}

// isGitIgnoredSet reports whether relPath — or any directory above it — is in
// the ignored set.
//
// Walking the ancestors is essential, not defensive. `git ls-files --others
// --ignored` collapses a fully-ignored directory into a *single* entry for the
// directory itself ("tmp/pi/") instead of listing the files inside it. An exact
// per-file lookup therefore matches nothing under such a directory, so every
// file in it was mined and embedded — and embedding is where a mining run spends
// ~80% of its CPU.
func isGitIgnoredSet(relPath string, ignored map[string]bool) bool {
	if ignored == nil {
		return false
	}
	p := filepath.ToSlash(relPath)
	for {
		if ignored[p] {
			return true
		}
		i := strings.LastIndexByte(p, '/')
		if i <= 0 {
			return false
		}
		p = p[:i]
	}
}
