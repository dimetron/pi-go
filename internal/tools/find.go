package tools

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
)

const maxFindResults = 500

// FindInput defines the parameters for the find tool.
type FindInput struct {
	// The glob pattern to match files against (e.g. "**/*.go", "*.ts").
	Pattern string `json:"pattern"`
	// The directory to search in. Defaults to current directory.
	Path string `json:"path,omitempty"`
}

// FindOutput contains the matching file paths.
type FindOutput struct {
	// List of matching file paths.
	Files []string `json:"files"`
	// Total matches found (may be more than returned if truncated).
	TotalFiles int `json:"total_files"`
	// Whether results were truncated due to limits.
	Truncated bool `json:"truncated,omitempty"`
}

func newFindTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("find", "Find files matching a glob pattern. Searches recursively through directories. Supports patterns like '*.go', '**/*.ts', 'src/**/*.test.js'.", func(_ agent.Context, input FindInput) (FindOutput, error) {
		return findHandler(sb, input)
	}, map[string]string{"glob": "pattern"})
}

func findHandler(sb *Sandbox, input FindInput) (FindOutput, error) {
	if input.Pattern == "" {
		return FindOutput{}, fmt.Errorf("pattern is required")
	}

	searchPath := input.Path
	if searchPath == "" {
		searchPath = "."
	}

	fsys := sb.FS()
	rel, err := sb.Resolve(searchPath)
	if err != nil {
		return FindOutput{}, err
	}

	// Load .gitignore patterns
	patterns, err := sb.LoadGitignorePatterns()
	if err != nil {
		// Non-fatal: continue without filtering
		patterns = nil
	}

	// Normalize doublestar patterns: since WalkDir already recurses,
	// strip "**/" prefixes so filepath.Match can handle the rest.
	// e.g. "**/*.go" → "*.go", "src/**/*.go" → "src/**/*.go" (handled below)
	w := &findWalk{
		root:        rel,
		pattern:     input.Pattern,
		filePattern: normalizeGlobPattern(input.Pattern),
		patterns:    patterns,
	}

	_ = fs.WalkDir(fsys, rel, w.visit)

	return FindOutput{
		Files:      w.files,
		TotalFiles: w.total,
		Truncated:  w.total > len(w.files),
	}, nil
}

// findWalk carries the state one find walk accumulates. Holding it here keeps
// the WalkDir callback a named method at nesting depth zero instead of a
// closure buried inside findHandler.
type findWalk struct {
	root        string // walk root, relative to the sandbox
	pattern     string // the caller's pattern, as given
	filePattern string // pattern with leading "**/" stripped
	patterns    []GitignorePattern
	files       []string
	total       int
}

// visit is the fs.WalkDir callback: it prunes skipped directories, drops
// gitignored and non-matching files, and records the rest.
func (w *findWalk) visit(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return nil
	}
	if d.IsDir() {
		// Skip always-ignored directories, and any the .gitignore prunes.
		if shouldSkipDir(d.Name()) || shouldSkipPath(path, d, w.patterns) {
			return filepath.SkipDir
		}
		return nil
	}

	// Apply .gitignore file filters
	if shouldSkipPath(path, d, w.patterns) {
		return nil
	}
	if !w.matches(path, d.Name()) {
		return nil
	}

	w.total++
	if len(w.files) < maxFindResults {
		w.files = append(w.files, path)
	}
	return nil
}

// matches reports whether a file is a hit: by filename first, then by path
// relative to the walk root, then by doublestar expansion of that path.
func (w *findWalk) matches(path, name string) bool {
	// Match against the filename using the normalized pattern
	if matched, _ := filepath.Match(w.filePattern, name); matched {
		return true
	}

	// Try matching against relative path for patterns like "src/*.go"
	relPath, relErr := filepath.Rel(w.root, path)
	if relErr != nil {
		return false
	}
	if matched, _ := filepath.Match(w.filePattern, relPath); matched {
		return true
	}

	// For patterns like "src/**/*.go", match each path segment
	return matchDoublestar(w.pattern, relPath)
}

// shouldSkipDir returns true for directories that should always be skipped.
func shouldSkipDir(base string) bool {
	// Explicitly do NOT skip .pi-go, .cursor, .claude - these contain agent/skill files
	agentDirs := map[string]bool{".pi-go": true, ".cursor": true, ".claude": true}
	if strings.HasPrefix(base, ".") && base != "." && !agentDirs[base] {
		return true
	}
	return base == "node_modules" || base == "vendor" || base == "__pycache__"
}

// normalizeGlobPattern strips leading "**/" from glob patterns since WalkDir
// already recurses into all directories. e.g. "**/*.go" → "*.go".
func normalizeGlobPattern(pattern string) string {
	for strings.HasPrefix(pattern, "**/") {
		pattern = pattern[3:]
	}
	return pattern
}

// matchDoublestar handles glob patterns containing "**" by splitting on "**/"
// and checking that each segment matches the corresponding part of the path.
// e.g. "src/**/*.go" matches "src/pkg/main.go".
func matchDoublestar(pattern, path string) bool {
	parts := strings.Split(pattern, "**/")
	if len(parts) < 2 {
		return false
	}

	// The prefix before ** must match the start of the path
	prefix := parts[0]
	if prefix != "" {
		prefix = strings.TrimSuffix(prefix, "/")
		if !strings.HasPrefix(path, prefix+"/") && path != prefix {
			return false
		}
	}

	// The suffix after the last ** must match the filename
	suffix := parts[len(parts)-1]
	if suffix != "" {
		name := filepath.Base(path)
		matched, _ := filepath.Match(suffix, name)
		return matched
	}
	return true
}
