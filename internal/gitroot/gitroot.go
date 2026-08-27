// Package gitroot resolves the repository root that a sandbox should cover.
//
// The distinction this package exists for: `git rev-parse --show-toplevel`
// run from inside a *linked worktree* returns the worktree directory, not the
// main checkout. pi-go spawns subagents whose working directory is a linked
// worktree (`.pi-go/tasks/<id>`, `.worktrees/<branch>`), and each spawned pi
// process derives its own PI_SANDBOX_ROOT. Using the toplevel there roots the
// sandbox at the worktree, so every read or edit of a path elsewhere in the
// repo is rejected as escaping the sandbox — which is what made /run workers
// fall back to shelling out instead of using the file tools.
//
// Detect resolves the main checkout instead, so a worker in a worktree can
// still reach the whole repository.
package gitroot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// CmdTimeout bounds every git subprocess this package spawns so a stalled
// repository (blocked hook, lock contention, unreachable network mount) cannot
// hang initialization indefinitely.
const CmdTimeout = 5 * time.Second

// Detect returns the main checkout of the repository containing dir, or "" when
// dir is not inside a repository.
//
// For an ordinary checkout this is the same as `git rev-parse --show-toplevel`.
// For a linked worktree it is the main checkout the worktree belongs to,
// resolved through the common git directory.
func Detect(ctx context.Context, dir string) string {
	top := revParse(ctx, dir, "--show-toplevel")
	if top == "" {
		return ""
	}
	if main := mainCheckout(ctx, dir); main != "" {
		return main
	}
	return top
}

// Toplevel returns the working tree containing dir — the linked worktree itself
// when dir is inside one. Callers that mean "the directory this checkout owns"
// rather than "the repository" want this.
func Toplevel(ctx context.Context, dir string) string {
	return revParse(ctx, dir, "--show-toplevel")
}

// mainCheckout derives the main checkout from the common git directory. It
// returns "" when the layout is anything other than a plain `<checkout>/.git`
// — a bare repository, a separate GIT_DIR, or a git too old for
// --path-format — leaving the caller with the toplevel it already has.
func mainCheckout(ctx context.Context, dir string) string {
	common := revParse(ctx, dir, "--path-format=absolute", "--git-common-dir")
	if common == "" || filepath.Base(common) != ".git" {
		return ""
	}
	parent := filepath.Dir(common)
	st, err := os.Stat(parent)
	if err != nil || !st.IsDir() {
		return ""
	}
	return parent
}

func revParse(ctx context.Context, dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, CmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"rev-parse"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
