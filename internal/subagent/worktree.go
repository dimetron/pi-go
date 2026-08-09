package subagent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// WorktreeManager manages git worktrees for isolated subagent execution.
type WorktreeManager struct {
	repoRoot string
	active   map[string]worktreeInfo // agentID → info
	mu       sync.Mutex
	inflight sync.WaitGroup // tracks in-flight Cleanup calls
	closed   bool           // set by CleanupAll to reject late Cleanup calls
}

type worktreeInfo struct {
	Path     string // Filesystem path to the worktree
	Branch   string // Branch name created for the worktree
	StashMsg string // Stash message created on Create, "" if nothing stashed
}

// NewWorktreeManager creates a new WorktreeManager rooted at the given git repo.
func NewWorktreeManager(repoRoot string) *WorktreeManager {
	return &WorktreeManager{
		repoRoot: repoRoot,
		active:   make(map[string]worktreeInfo),
	}
}

// RepoRoot returns the git repository root path.
func (m *WorktreeManager) RepoRoot() string {
	return m.repoRoot
}

// shortID returns a short suffix from an agent ID for use in paths and branch names.
// Agent IDs have the form "type-nanotimestamp", so we take the last 12 characters
// to get the unique timestamp portion.
func shortID(agentID string) string {
	if len(agentID) > 12 {
		return agentID[len(agentID)-12:]
	}
	return agentID
}

var nonWorktreeNameChars = regexp.MustCompile(`[^a-z0-9._-]+`)

func sanitizeWorktreeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, string(filepath.Separator), "-")
	name = strings.ReplaceAll(name, "/", "-")
	name = nonWorktreeNameChars.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-.")
	name = strings.Trim(strings.ReplaceAll(name, "--", "-"), "-.")
	if name == "" {
		return ""
	}
	return name
}

func worktreeNames(agentID, requested string) (pathID, branch string) {
	if name := sanitizeWorktreeName(requested); name != "" {
		return name, name
	}
	sid := shortID(agentID)
	return sid, "pi-agent-" + sid
}

// stashMessage returns a unique stash message so we can find and pop
// only the entry this Create call created.
func stashMessage(agentID string) string {
	return fmt.Sprintf("pi-go-worktree-%s-%d", agentID, time.Now().UnixNano())
}

// stashIndexAfter returns the number of stash entries present *before*
// we ran the stash push, so we can tell whether push actually created one.
func (m *WorktreeManager) stashIndexAfter() (int, error) {
	out, err := m.git("stash", "list")
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(out) == "" {
		return 0, nil
	}
	return len(strings.Split(out, "\n")), nil
}

// popStashByMessage pops the most recent stash entry whose message matches.
// Falls back to a plain `stash pop` if the by-message approach fails.
func (m *WorktreeManager) popStashByMessage(msg string) error {
	// `git stash pop` is destructive and may collide with new edits.
	// Try `git stash apply` first so the entry survives if anything goes
	// wrong; then drop it explicitly.
	if out, err := m.git("stash", "apply", "--quiet", "stash@{0}"); err != nil {
		// If apply failed, the entry is still in the stash list — return the
		// error but don't drop, so the user can recover manually.
		return fmt.Errorf("git stash apply: %w: %s", err, out)
	}
	// Best-effort drop. If drop fails (rare), the entry stays in the list
	// and the user can clean up with `git stash drop`.
	_, _ = m.git("stash", "drop", "--quiet", "stash@{0}")
	return nil
}

// Create creates a new git worktree for the given agent ID.
// If the current branch has uncommitted changes, they are stashed before
// creating the worktree and tracked in `pendingStash` so that MergeBack
// or Cleanup can pop them later (with a unique message).
// Returns the filesystem path to the worktree.
func (m *WorktreeManager) Create(agentID string, requestedName ...string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return "", fmt.Errorf("worktree manager is shut down")
	}

	if _, exists := m.active[agentID]; exists {
		return "", fmt.Errorf("worktree already exists for agent %s", agentID)
	}

	name := ""
	if len(requestedName) > 0 {
		name = requestedName[0]
	}
	pathID, branch := worktreeNames(agentID, name)
	wtPath := filepath.Join(m.repoRoot, ".pi-go", "tasks", pathID)

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return "", fmt.Errorf("creating worktree parent dir: %w", err)
	}

	// Verify whether the branch and/or worktree already exist before issuing
	// `git worktree add -b`. Re-running /run on the same spec produces a
	// deterministic branch name, so a prior run that left the branch behind
	// (e.g. after a failed merge) would otherwise cause `worktree add -b`
	// to fail with "a branch named X already exists".
	branchExists := m.branchExists(branch)
	attachedAt := m.worktreeForBranch(branch)

	// If the branch is already attached to a worktree, reuse it when the
	// path matches what we would have chosen; otherwise refuse rather than
	// silently overwriting a live worktree.
	if branchExists && attachedAt != "" {
		if !samePath(attachedAt, wtPath) {
			return "", fmt.Errorf("branch %q is already checked out at %s; cannot reuse for agent %s", branch, attachedAt, agentID)
		}
		m.active[agentID] = worktreeInfo{Path: wtPath, Branch: branch}
		return wtPath, nil
	}

	// If a stale worktree directory exists without a linked branch, prune
	// the metadata and remove the leftover directory so `worktree add`
	// can recreate cleanly.
	if _, statErr := os.Stat(wtPath); statErr == nil && attachedAt == "" {
		_, _ = m.git("worktree", "prune")
		_ = os.RemoveAll(wtPath)
	}

	// Stash any uncommitted changes before creating the worktree.
	// git worktree add HEAD fails if the working tree is dirty.
	// Use -u to also stash untracked files.
	//
	// Exit code semantics (stable across git versions and locales):
	//   0   — changes stashed
	//   1   — nothing to stash (clean tree)
	//   128 — fatal (e.g. not a git repo, corrupt repo)
	//
	// We detect "did stash happen" by counting stash entries before/after
	// rather than by string-matching the (locale-dependent) output.
	stashMsg := stashMessage(agentID)
	beforeCount, _ := m.stashIndexAfter()
	stashOut, stashErr := m.git("stash", "push", "-u", "-m", stashMsg)
	if stashErr != nil {
		var exitErr *exec.ExitError
		if errors.As(stashErr, &exitErr) && exitErr.ExitCode() == 1 {
			// Nothing to stash — proceed without tracking a stash entry.
			stashErr = nil
		} else {
			return "", fmt.Errorf("git stash before worktree: %w (output: %s)", stashErr, stashOut)
		}
	}
	afterCount, _ := m.stashIndexAfter()
	stashedSomething := stashErr == nil && afterCount > beforeCount

	// If the branch already exists but is not checked out anywhere, attach
	// it without `-b`. Otherwise create a fresh branch from HEAD.
	var out string
	var err error
	if branchExists {
		out, err = m.git("worktree", "add", wtPath, branch)
	} else {
		out, err = m.git("worktree", "add", "-b", branch, wtPath, "HEAD")
	}
	if err != nil {
		// Restore stashed changes on failure. We do this regardless of
		// whether `beforeCount` showed entries — if anything landed in
		// the stash list, drop it so the user isn't left with orphaned
		// entries after a failed Create.
		if stashedSomething {
			_, _ = m.git("stash", "drop", "--quiet", "stash@{0}")
		}
		return "", fmt.Errorf("git worktree add: %w: %s", err, out)
	}

	// Stash survives on success — MergeBack/Cleanup will pop it via
	// pendingStash. Storing the message here makes the pop deterministic
	// even if other stash entries appear between Create and pop.
	info := worktreeInfo{Path: wtPath, Branch: branch}
	if stashedSomething {
		info.StashMsg = stashMsg
	}
	m.active[agentID] = info
	return wtPath, nil
}

// CommitAll snapshots everything an agent left in its worktree onto the
// worktree branch, and reports whether there was anything to snapshot.
//
// Nothing else in this package commits, and agents are told their edits simply
// stay local to the worktree (internal/subagent/bundled/task.md), so a worktree
// branch otherwise sits at the commit it was created from. Every downstream
// step is defined in terms of commits: CreateBackupBranch points a ref at the
// branch tip, and MergeBack runs `git merge --no-ff`. Against a branch with no
// commits, the backup preserves nothing and the merge is a no-op — and then
// Cleanup runs `git worktree remove --force` and the work is gone. Committing
// first is what gives both of those something to act on.
//
// It is written with plumbing (write-tree/commit-tree) rather than
// `git commit` so that it runs no hooks and no signing: this is a machine
// snapshot taken to avoid losing data, and a failing pre-commit hook on
// half-finished agent work must not be able to turn that into a total loss.
// The merge the caller makes afterwards still follows the user's own config.
func (m *WorktreeManager) CommitAll(agentID, message string) (bool, error) {
	m.mu.Lock()
	info, exists := m.active[agentID]
	m.mu.Unlock()
	if !exists {
		var err error
		info, err = m.recoverWorktreeInfo(agentID)
		if err != nil {
			return false, fmt.Errorf("no worktree found for agent %s", agentID)
		}
	}

	status, err := m.gitIn(info.Path, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("worktree status: %w: %s", err, status)
	}
	if status == "" {
		return false, nil
	}

	if out, err := m.gitIn(info.Path, "add", "-A"); err != nil {
		return false, fmt.Errorf("staging worktree changes: %w: %s", err, out)
	}
	tree, err := m.gitIn(info.Path, "write-tree")
	if err != nil {
		return false, fmt.Errorf("writing worktree tree: %w: %s", err, tree)
	}
	parent, err := m.gitIn(info.Path, "rev-parse", "HEAD")
	if err != nil {
		return false, fmt.Errorf("resolving worktree HEAD: %w: %s", err, parent)
	}
	if strings.TrimSpace(message) == "" {
		message = "pi-go agent " + shortID(agentID)
	}
	commit, err := m.gitIn(info.Path, "commit-tree", tree, "-p", parent, "-m", message)
	if err != nil {
		return false, fmt.Errorf("creating worktree commit: %w: %s", err, commit)
	}
	if out, err := m.gitIn(info.Path, "update-ref", "HEAD", commit); err != nil {
		return false, fmt.Errorf("advancing worktree branch: %w: %s", err, out)
	}
	return true, nil
}

// CreateBackupBranch creates a permanent branch pointing at the worktree branch.
// The backup survives Cleanup, which removes the temporary worktree branch.
func (m *WorktreeManager) CreateBackupBranch(agentID, backupBranch string) error {
	m.mu.Lock()
	info, exists := m.active[agentID]
	m.mu.Unlock()
	if !exists {
		var err error
		info, err = m.recoverWorktreeInfo(agentID)
		if err != nil {
			return fmt.Errorf("no worktree found for agent %s", agentID)
		}
	}
	if strings.TrimSpace(backupBranch) == "" {
		return fmt.Errorf("backup branch is required")
	}
	if _, err := m.git("branch", "-f", backupBranch, info.Branch); err != nil {
		return fmt.Errorf("create backup branch %q: %w", backupBranch, err)
	}
	return nil
}

// Cleanup removes the worktree and branch for the given agent ID.
// After CleanupAll has started (closed=true), late Cleanup calls are
// no-ops — CleanupAll owns all remaining entries at that point.
func (m *WorktreeManager) Cleanup(agentID string) error {
	m.mu.Lock()
	if m.closed {
		// CleanupAll is running or has run; it will handle this entry.
		m.mu.Unlock()
		return nil
	}
	m.inflight.Add(1)
	info, exists := m.active[agentID]
	if !exists {
		m.mu.Unlock()
		m.inflight.Done()
		return fmt.Errorf("no worktree found for agent %s", agentID)
	}
	// Remove from active map under lock to prevent concurrent cleanup of the
	// same worktree (e.g. from completion goroutine and Shutdown racing).
	delete(m.active, agentID)
	m.mu.Unlock()

	err := m.cleanupWorktree(agentID, info)
	m.inflight.Done()
	return err
}

// cleanupWorktree performs the git operations to remove a worktree and its branch.
// If cleanup fails, the entry is re-added to the active map so callers can retry.
// If a stash was created during Create and not yet popped (e.g. MergeBack was
// never called), the stash entry is best-effort dropped here so it doesn't
// accumulate in the stash list.
func (m *WorktreeManager) cleanupWorktree(agentID string, info worktreeInfo) error {
	var errs []string

	// Remove the worktree only if the path still exists on disk.
	if _, statErr := os.Stat(info.Path); statErr == nil {
		if out, err := m.git("worktree", "remove", "--force", info.Path); err != nil {
			errs = append(errs, fmt.Sprintf("worktree remove: %v: %s", err, out))
			// Fallback: remove directory manually, then prune stale worktree
			// metadata so git no longer considers the branch "checked out".
			_ = os.RemoveAll(info.Path)
			_, _ = m.git("worktree", "prune")
		}
	} else {
		// Path already gone (e.g. prior partial cleanup) — prune stale
		// worktree metadata so git branch -D can succeed.
		_, _ = m.git("worktree", "prune")
	}

	// Delete the branch only if it still exists.
	if out, err := m.git("branch", "-D", info.Branch); err != nil {
		// Ignore "not found" — branch was already deleted on a prior attempt.
		if !strings.Contains(out, "not found") {
			errs = append(errs, fmt.Sprintf("branch delete: %v: %s", err, out))
		}
	}

	// Best-effort: drop any stash entry this worktree created. We do this
	// after the worktree/branch are removed because applying a stash to a
	// dirty working tree would just re-introduce the conflict.
	if info.StashMsg != "" {
		if entry, found := m.findStashByMessage(info.StashMsg); found {
			_, _ = m.git("stash", "drop", "--quiet", entry)
		}
	}

	if len(errs) > 0 {
		// Re-add entry so a retry pass can attempt again.
		m.mu.Lock()
		m.active[agentID] = info
		m.mu.Unlock()
		return fmt.Errorf("cleanup errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// MergeBack merges the worktree branch back into the current branch of the
// main worktree. Returns the merge output.
//
// On a successful merge, any stash created during Create is applied back
// to the main repo and dropped from the stash list.
//
// On a merge conflict, the merge is aborted (so the main repo is left in a
// clean state) and the worktree is preserved so the user can inspect and
// resolve conflicts manually.
func (m *WorktreeManager) MergeBack(agentID string) (string, error) {
	m.mu.Lock()
	info, exists := m.active[agentID]
	m.mu.Unlock()

	if !exists {
		var err error
		info, err = m.recoverWorktreeInfo(agentID)
		if err != nil {
			return "", fmt.Errorf("no worktree found for agent %s", agentID)
		}
	}

	out, err := m.git("merge", "--no-ff", info.Branch, "-m", fmt.Sprintf("Merge subagent %s", shortID(agentID)))
	if err != nil {
		// Check for merge conflicts — abort the merge so the main repo is
		// left clean, then preserve the worktree for manual resolution.
		if strings.Contains(out, "merge failed") || strings.Contains(out, "CONFLICT") {
			if abortOut, abortErr := m.git("merge", "--abort"); abortErr != nil {
				return out, fmt.Errorf("merge conflict and abort failed: %w (merge output: %s, abort output: %s)", err, out, abortOut)
			}
			return out, fmt.Errorf("merge conflict: changes not merged. Worktree preserved at %s for manual resolution: %w", info.Path, err)
		}
		return out, fmt.Errorf("merge failed: %w: %s", err, out)
	}

	// Merge succeeded — restore the stash entry created during Create.
	// We do this only if a stash was recorded; otherwise the main repo
	// is already clean.
	if info.StashMsg != "" {
		if entry, found := m.findStashByMessage(info.StashMsg); found {
			if applyErr := m.popStashByMessage(info.StashMsg); applyErr != nil {
				// Stash apply failed (likely conflicts with the merge).
				// Drop the entry anyway so the stash list stays bounded —
				// the user can recover via `git stash show` + cherry-pick
				// or by checking the worktree branch.
				_, _ = m.git("stash", "drop", "--quiet", entry)
				return out, fmt.Errorf("merge succeeded but stash apply failed: %w (merge output: %s)", applyErr, out)
			}
		}
	}

	// Merge succeeded — worktree is kept for post-merge inspection.
	// Caller should call Cleanup separately when ready.
	return out, nil
}

// findStashByMessage returns the stash ref (e.g. "stash@{0}") whose message
// matches msg, or "" + false if no match is found.
//
// Note: `git stash list --format=%s` renders the subject as
// "On <branch>: <original-message>", so we strip that prefix before comparing.
func (m *WorktreeManager) findStashByMessage(msg string) (string, bool) {
	if msg == "" {
		return "", false
	}
	out, err := m.git("stash", "list", "--format=%gd|%s")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(out, "\n") {
		// Format: "stash@{N}|On <branch>: <message>"
		sep := strings.Index(line, "|")
		if sep < 0 {
			continue
		}
		ref := line[:sep]
		subject := line[sep+1:]
		// Strip "On <branch>: " prefix.
		if colon := strings.Index(subject, ": "); colon >= 0 {
			subject = subject[colon+2:]
		}
		if subject == msg {
			return ref, true
		}
	}
	return "", false
}

func (m *WorktreeManager) recoverWorktreeInfo(agentID string) (worktreeInfo, error) {
	sid := shortID(agentID)
	info := worktreeInfo{
		Path:   filepath.Join(m.repoRoot, ".pi-go", "tasks", sid),
		Branch: "pi-agent-" + sid,
	}

	// Verify the branch is actually attached to the expected worktree
	// (or any worktree). An orphaned branch (one whose worktree was
	// removed manually) should NOT be silently re-attached — that would
	// resurrect a "deleted" worktree without the user's intent.
	attachedAt := m.worktreeForBranch(info.Branch)
	if attachedAt != "" {
		if samePath(attachedAt, info.Path) {
			return info, nil
		}
		return worktreeInfo{}, fmt.Errorf("branch %q is checked out at %s, not the expected %s; refusing to recover", info.Branch, attachedAt, info.Path)
	}

	// No worktree is attached to this branch. Refuse recovery so we don't
	// silently re-create a worktree the user already cleaned up.
	return worktreeInfo{}, fmt.Errorf("worktree metadata not found for agent %s (branch %q is orphaned)", agentID, info.Branch)
}

// CleanupAll removes all active worktrees. Used during shutdown.
// It sets the closed flag to reject late Cleanup calls from completion
// goroutines, waits for in-flight cleanups, then retries remaining
// entries up to maxPasses.
func (m *WorktreeManager) CleanupAll() error {
	// Prevent new Cleanup calls from starting — after this point,
	// completion goroutines that call Cleanup will no-op.
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()

	// Wait for any in-flight Cleanup calls that started before we set closed.
	m.inflight.Wait()

	const maxPasses = 3
	var lastErrs []string

	for pass := range maxPasses {
		m.mu.Lock()
		snapshot := make(map[string]worktreeInfo, len(m.active))
		for id, info := range m.active {
			snapshot[id] = info
			delete(m.active, id)
		}
		m.mu.Unlock()

		if len(snapshot) == 0 {
			return nil
		}

		lastErrs = nil
		for id, info := range snapshot {
			if err := m.cleanupWorktree(id, info); err != nil {
				lastErrs = append(lastErrs, fmt.Sprintf("agent %s: %v (pass %d)", id, err, pass+1))
			}
		}

		m.mu.Lock()
		remaining := len(m.active)
		m.mu.Unlock()
		if remaining == 0 {
			return nil
		}
	}

	if len(lastErrs) > 0 {
		return fmt.Errorf("cleanup errors after %d passes: %s", maxPasses, strings.Join(lastErrs, "; "))
	}
	return nil
}

// Active returns the number of active worktrees.
func (m *WorktreeManager) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.active)
}

// PathFor returns the worktree path for the given agent ID, or empty string if none.
func (m *WorktreeManager) PathFor(agentID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if info, ok := m.active[agentID]; ok {
		return info.Path
	}
	return ""
}

// git runs a git command in the repo root directory and returns combined output.
func (m *WorktreeManager) git(args ...string) (string, error) {
	return m.gitIn(m.repoRoot, args...)
}

// gitIn runs a git command in the given directory and returns combined output.
func (m *WorktreeManager) gitIn(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// branchExists reports whether a local branch with the given name exists.
func (m *WorktreeManager) branchExists(branch string) bool {
	if branch == "" {
		return false
	}
	out, err := m.git("branch", "--list", branch)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

// worktreeForBranch returns the worktree path that currently has the given
// branch checked out, or "" if the branch is not attached to any worktree.
func (m *WorktreeManager) worktreeForBranch(branch string) string {
	if branch == "" {
		return ""
	}
	out, err := m.git("worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	want := "refs/heads/" + branch
	var currentPath string
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			currentPath = strings.TrimSpace(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "branch "):
			if strings.TrimSpace(strings.TrimPrefix(line, "branch ")) == want {
				return currentPath
			}
		}
	}
	return ""
}

// samePath reports whether two filesystem paths refer to the same location,
// resolving symlinks (e.g. macOS reports /var/folders as /private/var/folders).
// Falls back to a literal string compare when paths cannot be resolved.
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return a == b
	}
	return ra == rb
}
