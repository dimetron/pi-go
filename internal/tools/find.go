package tools

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"google.golang.org/adk/tool"
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
	return newTool("find", "Find files matching a glob pattern. Searches recursively through directories. Supports patterns like '*.go', '**/*.ts', 'src/**/*.test.js'.", func(_ tool.Context, input FindInput) (FindOutput, error) {
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
	pattern := input.Pattern
	filePattern := normalizeGlobPattern(pattern)

	var files []string
	total := 0

	_ = fs.WalkDir(fsys, rel, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name()) {
				return filepath.SkipDir
			}
			// Apply .gitignore: if any pattern says skip this directory, skip it
			if shouldSkipPath(path, d, patterns) {
				return filepath.SkipDir
			}
			return nil
		}

		// Apply .gitignore file filters
		if shouldSkipPath(path, d, patterns) {
			return nil
		}

		// Match against the filename using the normalized pattern
		name := d.Name()
		matched, _ := filepath.Match(filePattern, name)
		if !matched {
			// Try matching against relative path for patterns like "src/*.go"
			relPath, relErr := filepath.Rel(rel, path)
			if relErr == nil {
				matched, _ = filepath.Match(filePattern, relPath)
				if !matched {
					// For patterns like "src/**/*.go", match each path segment
					matched = matchDoublestar(pattern, relPath)
				}
			}
		}

		if matched {
			total++
			if len(files) < maxFindResults {
				files = append(files, path)
			}
		}
		return nil
	})

	return FindOutput{
		Files:      files,
		TotalFiles: total,
		Truncated:  total > len(files),
	}, nil
}

// shouldSkipDir returns true for directories that should always be skipped.
func shouldSkipDir(base string) bool {
	return (strings.HasPrefix(base, ".") && base != ".") ||
		base == "node_modules" ||
		base == "vendor" ||
		base == "__pycache__"
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
