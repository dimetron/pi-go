package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"

	"github.com/dimetron/pi-go/internal/otel"
)

const defaultGitTimeout = 10 * time.Second

// GitOverviewInput defines the parameters for the git-overview tool.
type GitOverviewInput struct {
	// Include staged files in the output. Default: true.
	IncludeStaged *bool `json:"include_staged,omitempty"`
	// Include unstaged modified files in the output. Default: true.
	IncludeUnstaged *bool `json:"include_unstaged,omitempty"`
	// Include untracked files in the output. Default: true.
	IncludeUntracked *bool `json:"include_untracked,omitempty"`
}

// GitOverviewOutput contains the git repository overview.
type GitOverviewOutput struct {
	// Current branch name.
	Branch string `json:"branch"`
	// Recent commits (up to 10, most recent first).
	RecentCommits []string `json:"recent_commits"`
	// Files staged for commit (index column of porcelain).
	StagedFiles []string `json:"staged_files,omitempty"`
	// Files modified but not staged (worktree column of porcelain).
	UnstagedFiles []string `json:"unstaged_files,omitempty"`
	// Untracked files.
	UntrackedFiles []string `json:"untracked_files,omitempty"`
	// Upstream tracking branch (empty if none).
	Upstream string `json:"upstream,omitempty"`
	// Commits ahead of upstream.
	Ahead int `json:"ahead"`
	// Commits behind upstream.
	Behind int `json:"behind"`
}

func newGitOverviewTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("git-overview", "Get an overview of the current git repository: branch, recent commits, staged/unstaged/untracked files, and upstream status.", func(ctx agent.ToolContext, input GitOverviewInput) (GitOverviewOutput, error) {
		return gitOverviewHandler(sb, ctx, input)
	})
}

func gitOverviewHandler(sb *Sandbox, ctx agent.ToolContext, input GitOverviewInput) (GitOverviewOutput, error) {
	dir := sb.Dir()

	var parentCtx = context.Background()
	if ctx != nil {
		parentCtx = ctx
	}
	_, span := otel.Tracer("pi-go").Start(parentCtx, "tool.git-overview")
	span.SetAttributes(attribute.String("git.dir", dir))
	defer span.End()

	// Check if directory is a git repo
	if _, err := runGit(ctx, dir, "rev-parse", "--git-dir"); err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.String("git.error", "not_a_git_repository"))
		return GitOverviewOutput{}, fmt.Errorf("not a git repository: %w", err)
	}

	var out GitOverviewOutput

	// Branch name
	if branch, err := runGit(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		out.Branch = strings.TrimSpace(branch)
	}

	// Recent commits (may fail in empty repo)
	if log, err := runGit(ctx, dir, "log", "--oneline", "-10"); err == nil {
		trimmed := strings.TrimSpace(log)
		if trimmed != "" {
			out.RecentCommits = strings.Split(trimmed, "\n")
		}
	}

	// Defaults: include everything unless explicitly set to false
	includeStaged := input.IncludeStaged == nil || *input.IncludeStaged
	includeUnstaged := input.IncludeUnstaged == nil || *input.IncludeUnstaged
	includeUntracked := input.IncludeUntracked == nil || *input.IncludeUntracked

	// Parse porcelain status
	if status, err := runGit(ctx, dir, "status", "--porcelain"); err == nil {
		staged, unstaged, untracked := parsePorcelain(status)
		if includeStaged {
			out.StagedFiles = staged
		}
		if includeUnstaged {
			out.UnstagedFiles = unstaged
		}
		if includeUntracked {
			out.UntrackedFiles = untracked
		}
	}

	// Upstream tracking info
	if upstream, err := runGit(ctx, dir, "rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
		out.Upstream = strings.TrimSpace(upstream)

		// Ahead/behind counts
		if counts, err := runGit(ctx, dir, "rev-list", "--left-right", "--count", "@{upstream}...HEAD"); err == nil {
			parts := strings.Fields(strings.TrimSpace(counts))
			if len(parts) == 2 {
				out.Behind, _ = strconv.Atoi(parts[0])
				out.Ahead, _ = strconv.Atoi(parts[1])
			}
		}
	}

	span.SetAttributes(
		attribute.String("git.branch", out.Branch),
		attribute.Int("git.staged_count", len(out.StagedFiles)),
		attribute.Int("git.unstaged_count", len(out.UnstagedFiles)),
		attribute.Int("git.untracked_count", len(out.UntrackedFiles)),
		attribute.Int("git.commit_count", len(out.RecentCommits)),
	)
	return out, nil
}

// parsePorcelain parses `git status --porcelain` output into staged, unstaged, and untracked file lists.
func parsePorcelain(output string) (staged, unstaged, untracked []string) {
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 3 {
			continue
		}
		x := line[0] // index (staging area) status
		y := line[1] // worktree status
		file := strings.TrimSpace(line[2:])
		// Handle renames: "R  old -> new"
		if idx := strings.Index(file, " -> "); idx >= 0 {
			file = file[idx+4:]
		}

		if x == '?' && y == '?' {
			untracked = append(untracked, file)
			continue
		}
		// Staged: index column has a non-space, non-? letter
		if x != ' ' && x != '?' {
			staged = append(staged, string(x)+" "+file)
		}
		// Unstaged: worktree column has a non-space, non-? letter
		if y != ' ' && y != '?' {
			unstaged = append(unstaged, string(y)+" "+file)
		}
	}
	return
}

// runGit executes a git command in the given directory and returns stdout.
func runGit(ctx agent.ToolContext, dir string, args ...string) (string, error) {
	var parentCtx = context.Background()
	if ctx != nil {
		parentCtx = ctx
	}
	cmdCtx, cancel := context.WithTimeout(parentCtx, defaultGitTimeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "git", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return stdout.String(), nil
}
