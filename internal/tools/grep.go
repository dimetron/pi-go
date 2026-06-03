package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
)

const maxGrepMatches = 200

// regexCache stores compiled regex patterns to avoid recompilation.
// Uses LRU-style eviction when max entries is reached.
type regexCache struct {
	mu      sync.Mutex
	entries map[string]*cachedRegex
	maxSize int
	maxAge  time.Duration
}

type cachedRegex struct {
	re      *regexp.Regexp
	created time.Time
}

func newRegexCache(maxSize int, maxAge time.Duration) *regexCache {
	return &regexCache{
		entries: make(map[string]*cachedRegex),
		maxSize: maxSize,
		maxAge:  maxAge,
	}
}

func (c *regexCache) get(key string) *regexp.Regexp {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil
	}
	if time.Since(entry.created) > c.maxAge {
		delete(c.entries, key)
		return nil
	}
	return entry.re
}

func (c *regexCache) put(key string, re *regexp.Regexp) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Evict oldest if at capacity
	if len(c.entries) >= c.maxSize {
		var oldest string
		var oldestTime time.Time
		for k, e := range c.entries {
			if oldestTime.IsZero() || e.created.Before(oldestTime) {
				oldest = k
				oldestTime = e.created
			}
		}
		if oldest != "" {
			delete(c.entries, oldest)
		}
	}

	c.entries[key] = &cachedRegex{
		re:      re,
		created: time.Now(),
	}
}

// Global regex cache - shared across all grep calls.
var grepRegexCache = newRegexCache(50, 10*time.Minute)

// ripgrepAvailable checks if rg (ripgrep) is installed.
func ripgrepAvailable() bool {
	cmd := exec.Command("rg", "--version")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// rgAvailable is set at startup based on whether ripgrep is installed.
var rgAvailable = ripgrepAvailable()

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
	grepToolName := "grep"
	if rgAvailable {
		grepToolName = "ripgrep"
	}
	return newTool(grepToolName, "Search file contents using a regex pattern. Supports glob filtering and case-insensitive search. Returns matching lines with file paths and line numbers.", func(_ agent.ToolContext, input GrepInput) (GrepOutput, error) {
		return grepHandler(sb, input)
	})
}

func grepHandler(sb *Sandbox, input GrepInput) (GrepOutput, error) {
	if input.Pattern == "" {
		return GrepOutput{}, fmt.Errorf("pattern is required")
	}

	searchPath := input.Path
	if searchPath == "" {
		searchPath = "."
	}

	info, err := sb.Stat(searchPath)
	if err != nil {
		return GrepOutput{}, fmt.Errorf("path not found: %w", err)
	}

	// Try ripgrep first if available
	if rgAvailable && !grepRGDisabled {
		if result, err := grepWithRG(sb, input, searchPath); err == nil {
			return result, nil
		}
		// Fall back to Go implementation on error
	}

	// Build cache key including case-insensitive flag
	cacheKey := input.Pattern
	if input.CaseInsensitive {
		cacheKey = "(?i)" + cacheKey
	}

	// Try cache first
	re := grepRegexCache.get(cacheKey)
	if re == nil {
		flags := ""
		if input.CaseInsensitive {
			flags = "(?i)"
		}
		var err error
		re, err = regexp.Compile(flags + input.Pattern)
		if err != nil {
			return GrepOutput{}, fmt.Errorf("invalid regex pattern: %w", err)
		}
		grepRegexCache.put(cacheKey, re)
	}

	// Load .gitignore patterns
	patterns, err := sb.LoadGitignorePatterns()
	if err != nil {
		patterns = nil
	}

	var matches []GrepMatch
	total := 0

	if info.IsDir() {
		walkFn := func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // skip errors
			}
			if d.IsDir() {
				if shouldSkipDir(d.Name()) {
					return filepath.SkipDir
				}
				if shouldSkipPath(path, d, patterns) {
					return filepath.SkipDir
				}
				return nil
			}
			if shouldSkipPath(path, d, patterns) {
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
		_ = fs.WalkDir(fsys, rel, walkFn)
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

// grepWithRG runs ripgrep (rg) and parses its output into GrepOutput.
// Returns an error if rg fails or parsing fails.
// Note: Uses --no-ignore to explicitly search .pi-go, .cursor, .claude directories.
func grepWithRG(sb *Sandbox, input GrepInput, searchPath string) (GrepOutput, error) {
	ctx := context.Background()

	args := []string{
		"--no-heading",    // Show file:line:content format
		"--with-filename", // Always show filename
		"--line-number",   // Show line numbers
		"--no-ignore",     // Explicitly search .pi-go, .cursor, .claude directories
		"--max-count", fmt.Sprintf("%d", maxGrepMatches),
	}

	// Handle case insensitive
	if input.CaseInsensitive {
		args = append(args, "-i")
	}

	// Handle glob pattern
	if input.Glob != "" {
		args = append(args, "--glob", input.Glob)
	}

	// Add the pattern and path
	args = append(args, input.Pattern, searchPath)

	cmd := exec.CommandContext(ctx, "rg", args...)
	cmd.Dir = sb.Dir()

	output, err := cmd.Output()
	if err != nil {
		return GrepOutput{}, fmt.Errorf("rg failed: %w", err)
	}

	// Parse rg output: "file:line:content" format
	var matches []GrepMatch
	total := 0
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		file := parts[0]
		// Skip the filename line that rg adds with --with-filename on single files
		if strings.HasPrefix(file, "=== ") {
			continue
		}
		var lineNum int
		if _, err := fmt.Sscanf(parts[1], "%d", &lineNum); err != nil {
			continue
		}
		content := parts[2]
		total++
		if len(matches) < maxGrepMatches {
			matches = append(matches, GrepMatch{
				File:    file,
				Line:    lineNum,
				Content: truncateLine(content),
			})
		}
	}

	return GrepOutput{
		Matches:      matches,
		TotalMatches: total,
		Truncated:    total > len(matches),
	}, nil
}

// grepRGDisabled is set to true during tests to force use of the Go implementation.
var grepRGDisabled = false
