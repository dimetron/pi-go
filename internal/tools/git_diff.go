package tools

import (
	"fmt"
	"strings"

	"google.golang.org/adk/tool"
)

// GitFileDiffInput defines the parameters for the git-file-diff tool.
type GitFileDiffInput struct {
	// File path to diff (relative to repo root).
	File string `json:"file"`
	// If true, show staged (cached) diff instead of unstaged. Default: false.
	Staged bool `json:"staged,omitempty"`
}

// GitFileDiffOutput contains the unified diff for a single file.
type GitFileDiffOutput struct {
	// File path that was diffed.
	File string `json:"file"`
	// Unified diff output.
	Diff string `json:"diff"`
	// Diff statistics: lines added.
	LinesAdded int `json:"lines_added"`
	// Diff statistics: lines removed.
	LinesRemoved int `json:"lines_removed"`
	// True if the file is binary.
	Binary bool `json:"binary,omitempty"`
	// True if diff output was truncated.
	Truncated bool `json:"truncated,omitempty"`
}

func newGitFileDiffTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("git-file-diff", `Get the unified diff for a specific file.

Required: file (path relative to repo root).
Optional: staged (bool, default false — set true for cached/staged changes).`, func(ctx tool.Context, input GitFileDiffInput) (GitFileDiffOutput, error) {
		return gitFileDiffHandler(sb, ctx, input)
	})
}

func gitFileDiffHandler(sb *Sandbox, ctx tool.Context, input GitFileDiffInput) (GitFileDiffOutput, error) {
	if input.File == "" {
		return GitFileDiffOutput{}, fmt.Errorf("file is required")
	}

	dir := sb.Dir()

	// Check if directory is a git repo
	if _, err := runGit(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		return GitFileDiffOutput{}, fmt.Errorf("not a git repository")
	}

	args := []string{"diff"}
	if input.Staged {
		args = append(args, "--cached")
	}
	args = append(args, "--", input.File)

	diff, err := runGit(ctx, dir, args...)
	if err != nil {
		return GitFileDiffOutput{}, fmt.Errorf("git diff failed: %w", err)
	}

	out := GitFileDiffOutput{
		File: input.File,
	}

	// Check for binary file
	if strings.Contains(diff, "Binary files") {
		out.Binary = true
		out.Diff = "Binary file differs"
		return out, nil
	}

	// Count added/removed lines
	for _, line := range strings.Split(diff, "\n") {
		if len(line) > 0 && line[0] == '+' && !strings.HasPrefix(line, "+++") {
			out.LinesAdded++
		} else if len(line) > 0 && line[0] == '-' && !strings.HasPrefix(line, "---") {
			out.LinesRemoved++
		}
	}

	// Truncate if needed
	truncated := truncateOutput(diff)
	if len(truncated) < len(diff) {
		out.Truncated = true
	}
	out.Diff = truncated

	return out, nil
}
