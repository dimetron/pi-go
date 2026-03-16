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
	})
}

func findHandler(sb *Sandbox, input FindInput) (FindOutput, error) {
	if input.Pattern == "" {
		return FindOutput{}, fmt.Errorf("pattern is required")
	}

	searchPath := input.Path
	if searchPath == "" {
		searchPath = "."
	}

	var files []string
	total := 0

	fsys := sb.FS()
	rel, err := sb.Resolve(searchPath)
	if err != nil {
		return FindOutput{}, err
	}

	fs.WalkDir(fsys, rel, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if strings.HasPrefix(base, ".") && base != "." || base == "node_modules" || base == "vendor" || base == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}

		// Match against the filename and also the relative path
		name := d.Name()
		matched, _ := filepath.Match(input.Pattern, name)
		if !matched {
			// Try matching against relative path for patterns like "src/**/*.go"
			relPath, relErr := filepath.Rel(rel, path)
			if relErr == nil {
				matched, _ = filepath.Match(input.Pattern, relPath)
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
