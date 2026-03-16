package tools

import (
	"bufio"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"google.golang.org/adk/tool"
)

const maxGrepMatches = 200

// GrepInput defines the parameters for the grep tool.
type GrepInput struct {
	// The regex pattern to search for.
	Pattern string `json:"pattern"`
	// The file or directory to search in. Defaults to current directory.
	Path string `json:"path,omitempty"`
	// Glob pattern to filter files (e.g. "*.go", "*.{ts,tsx}").
	Glob string `json:"glob,omitempty"`
	// If true, perform case-insensitive matching.
	CaseInsensitive bool `json:"case_insensitive,omitempty"`
}

// GrepOutput contains the search results.
type GrepOutput struct {
	// List of matches with file path, line number, and content.
	Matches []GrepMatch `json:"matches"`
	// Total number of matches found (may be more than returned if truncated).
	TotalMatches int `json:"total_matches"`
	// Whether results were truncated due to limits.
	Truncated bool `json:"truncated,omitempty"`
}

// GrepMatch represents a single grep match.
type GrepMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

func newGrepTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("grep", "Search file contents using a regex pattern. Supports glob filtering and case-insensitive search. Returns matching lines with file paths and line numbers.", func(_ tool.Context, input GrepInput) (GrepOutput, error) {
		return grepHandler(sb, input)
	})
}

func grepHandler(sb *Sandbox, input GrepInput) (GrepOutput, error) {
	if input.Pattern == "" {
		return GrepOutput{}, fmt.Errorf("pattern is required")
	}

	flags := ""
	if input.CaseInsensitive {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + input.Pattern)
	if err != nil {
		return GrepOutput{}, fmt.Errorf("invalid regex pattern: %w", err)
	}

	searchPath := input.Path
	if searchPath == "" {
		searchPath = "."
	}

	info, err := sb.Stat(searchPath)
	if err != nil {
		return GrepOutput{}, fmt.Errorf("path not found: %w", err)
	}

	var matches []GrepMatch
	total := 0

	if info.IsDir() {
		walkFn := func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip errors
			}
			if d.IsDir() {
				base := d.Name()
				if strings.HasPrefix(base, ".") && base != "." || base == "node_modules" || base == "vendor" || base == "__pycache__" {
					return filepath.SkipDir
				}
				return nil
			}
			if input.Glob != "" {
				matched, _ := filepath.Match(input.Glob, d.Name())
				if !matched {
					return nil
				}
			}
			fileMatches := grepFileSandbox(sb, re, path)
			total += len(fileMatches)
			if len(matches) < maxGrepMatches {
				remaining := maxGrepMatches - len(matches)
				if len(fileMatches) > remaining {
					fileMatches = fileMatches[:remaining]
				}
				matches = append(matches, fileMatches...)
			}
			return nil
		}
		// Use sandbox ReadDir recursively via fs.WalkDir on the Root's FS
		fsys := sb.FS()
		rel, resolveErr := sb.Resolve(searchPath)
		if resolveErr != nil {
			return GrepOutput{}, resolveErr
		}
		fs.WalkDir(fsys, rel, walkFn)
	} else {
		matches = grepFileSandbox(sb, re, searchPath)
		total = len(matches)
		if len(matches) > maxGrepMatches {
			matches = matches[:maxGrepMatches]
		}
	}

	return GrepOutput{
		Matches:      matches,
		TotalMatches: total,
		Truncated:    total > len(matches),
	}, nil
}

func grepFileSandbox(sb *Sandbox, re *regexp.Regexp, path string) []GrepMatch {
	f, err := sb.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var matches []GrepMatch
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			matches = append(matches, GrepMatch{
				File:    path,
				Line:    lineNum,
				Content: truncateLine(line),
			})
		}
	}
	return matches
}
