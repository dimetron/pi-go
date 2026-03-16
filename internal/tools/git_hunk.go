package tools

import (
	"fmt"
	"strings"

	"google.golang.org/adk/tool"
)

// GitHunkInput defines the parameters for the git-hunk tool.
type GitHunkInput struct {
	// File path to inspect hunks for (relative to repo root).
	File string `json:"file"`
	// If true, show hunks from staged diff. Default: false.
	Staged bool `json:"staged,omitempty"`
}

// Hunk represents a single diff hunk with metadata.
type Hunk struct {
	// Hunk header (e.g. "@@ -1,3 +1,5 @@").
	Header string `json:"header"`
	// Raw hunk content (context + added + removed lines).
	Content string `json:"content"`
	// Number of lines added in this hunk.
	Added int `json:"added"`
	// Number of lines removed in this hunk.
	Removed int `json:"removed"`
}

// GitHunkOutput contains parsed hunks for a file.
type GitHunkOutput struct {
	// File path.
	File string `json:"file"`
	// Parsed hunks.
	Hunks []Hunk `json:"hunks"`
	// Total number of hunks.
	TotalHunks int `json:"total_hunks"`
}

func newGitHunkTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("git-hunk", "Get parsed diff hunks for a specific file. Each hunk includes its header, content, and line count statistics.", func(ctx tool.Context, input GitHunkInput) (GitHunkOutput, error) {
		return gitHunkHandler(sb, ctx, input)
	})
}

func gitHunkHandler(sb *Sandbox, ctx tool.Context, input GitHunkInput) (GitHunkOutput, error) {
	if input.File == "" {
		return GitHunkOutput{}, fmt.Errorf("file is required")
	}

	dir := sb.Dir()

	// Check if directory is a git repo
	if _, err := runGit(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		return GitHunkOutput{}, fmt.Errorf("not a git repository")
	}

	args := []string{"diff", "-U3"}
	if input.Staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", input.File)

	diff, err := runGit(ctx, dir, args...)
	if err != nil {
		return GitHunkOutput{}, fmt.Errorf("git diff failed: %w", err)
	}

	hunks := parseHunks(diff)

	return GitHunkOutput{
		File:       input.File,
		Hunks:      hunks,
		TotalHunks: len(hunks),
	}, nil
}

// parseHunks splits a unified diff into individual Hunk structs.
func parseHunks(diff string) []Hunk {
	if diff == "" {
		return nil
	}

	lines := strings.Split(diff, "\n")
	var hunks []Hunk
	var current *Hunk
	var contentLines []string

	for _, line := range lines {
		if strings.HasPrefix(line, "@@") {
			// Save previous hunk
			if current != nil {
				current.Content = strings.Join(contentLines, "\n")
				hunks = append(hunks, *current)
			}
			// Start new hunk
			current = &Hunk{Header: line}
			contentLines = nil
			continue
		}

		if current == nil {
			// Skip file header lines before first hunk
			continue
		}

		contentLines = append(contentLines, line)
		if len(line) > 0 {
			switch line[0] {
			case '+':
				current.Added++
			case '-':
				current.Removed++
			}
		}
	}

	// Save last hunk
	if current != nil {
		current.Content = strings.Join(contentLines, "\n")
		hunks = append(hunks, *current)
	}

	return hunks
}
