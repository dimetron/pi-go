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

	// gitRunner replaces the git exec when non-nil; nil means real git.
	// Create's recovery paths depend on what the repo looks like *between*
	// two of its git invocations (another process pushing a stash, a file
	// reappearing), which is otherwise unreachable from a test. Set it before
	// the manager is used; it is not guarded by mu.
	gitRunner func(dir string, args ...string) (string, error)
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

// The stash lifecycle
//
// Create takes a stash so that `git worktree add` sees a clean tree. That
// entry holds the *user's* uncommitted work — not the agent's, which lives on
// the worktree branch — and for as long as it exists the working tree no
// longer holds a copy of it. One rule follows, and every disposal site in this
// file obeys it:
//
//	An entry Create pushed may leave the stash list only after it has been
//	successfully applied back to the working tree. Every other exit — a failed
//	`worktree add`, a failed apply, a run abandoned before MergeBack — leaves
//	the entry where the user can see it and reports why.
//
// `git stash drop` on an entry that was never applied is therefore never
// correct here: it deletes the content instead of restoring it, leaving it
// reachable only as a dangling commit until gc. A stash list with one extra
// entry in it is cheap; the work inside that entry is not.

// errStashEntryGone reports that no stash entry carries the requested message.
// Callers for which a vanished entry is benign — MergeBack and cleanupWorktree
// both run after something else may already have restored it — filter it out
// with errors.Is. Create does not: it pushed the entry moments earlier, so its
// absence there means something went wrong.
var errStashEntryGone = errors.New("stash entry not found")

// popStashByMessage restores the stash entry whose message matches msg and
// then removes it from the stash list. A failed apply leaves the entry in
// place, per "The stash lifecycle" above.
//
// The entry is located by message rather than assumed to sit at stash@{0}:
// anything else with access to the repo — the user, an editor plugin, another
// pi-go agent — can push a stash between our push and our pop, and applying
// the top entry blindly would then restore someone else's work over the
// working tree while leaving ours behind. A message that matches no entry is
// reported as errStashEntryGone; it never falls back to whatever happens to be
// on top.
func (m *WorktreeManager) popStashByMessage(msg string) error {
	entry, found := m.findStashByMessage(msg)
	if !found {
		return fmt.Errorf("%w: %q", errStashEntryGone, msg)
	}
	// `git stash pop` is destructive and may collide with new edits.
	// Use `git stash apply` first so the entry survives if anything goes
	// wrong; then drop it explicitly.
	if out, err := m.git("stash", "apply", "--quiet", entry); err != nil {
		// If apply failed, the entry is still in the stash list — return the
		// error but don't drop, so the user can recover manually.
		return fmt.Errorf("git stash apply %s (%s): %w: %s", entry, msg, err, out)
	}
	// `git stash apply` leaves the stash list untouched, so `entry` still
	// addresses the same commit. Best-effort drop: if it fails (rare), the
	// entry stays in the list and the user can clean up with `git stash drop`.
	_, _ = m.git("stash", "drop", "--quiet", entry)
	return nil
}

// claimExistingWorktree decides what to do about a branch or directory that is
// already there before `git worktree add` runs.
//
// Re-running /run on the same spec produces a deterministic branch name, so a
// prior run that left the branch behind (e.g. after a failed merge) would
// otherwise make `worktree add -b` fail with "a branch named X already exists".
//
// It returns reuse=true when the branch is attached to exactly the worktree we
// would have created, and an error when it is attached somewhere else — the
// alternative there is silently overwriting a live worktree. Otherwise it
// prunes any stale leftovers so `worktree add` can recreate cleanly.
func (m *WorktreeManager) claimExistingWorktree(agentID, branch, wtPath string, branchExists bool, attachedAt string) (bool, error) {
	if branchExists && attachedAt != "" {
		if !samePath(attachedAt, wtPath) {
			return false, fmt.Errorf("branch %q is already checked out at %s; cannot reuse for agent %s", branch, attachedAt, agentID)
		}
		return true, nil
	}

	// If a stale worktree directory exists without a linked branch, prune
	// the metadata and remove the leftover directory so `worktree add`
	// can recreate cleanly.
	if _, statErr := os.Stat(wtPath); statErr == nil && attachedAt == "" {
		_, _ = m.git("worktree", "prune")
		_ = os.RemoveAll(wtPath)
	}
	return false, nil
}

// stashBeforeWorktreeAdd stashes any uncommitted changes before creating the
// worktree, because `git worktree add HEAD` fails if the working tree is dirty.
// Use -u to also stash untracked files.
//
// Exit code semantics (stable across git versions and locales):
//
//	0   — changes stashed
//	1   — nothing to stash (clean tree)
//	128 — fatal (e.g. not a git repo, corrupt repo)
//
// We detect "did stash happen" by counting stash entries before/after rather
// than by string-matching the (locale-dependent) output. The returned message
// is the unique one used for the push, so the eventual pop can find exactly
// this entry; stashed reports whether an entry was actually created.
func (m *WorktreeManager) stashBeforeWorktreeAdd(agentID string) (msg string, stashed bool, err error) {
	stashMsg := stashMessage(agentID)
	beforeCount, _ := m.stashIndexAfter()
	stashOut, stashErr := m.git("stash", "push", "-u", "-m", stashMsg)
	if stashErr != nil {
		var exitErr *exec.ExitError
		if errors.As(stashErr, &exitErr) && exitErr.ExitCode() == 1 {
			// Nothing to stash — proceed without tracking a stash entry.
			stashErr = nil
		} else {
			return "", false, fmt.Errorf("git stash before worktree: %w (output: %s)", stashErr, stashOut)
		}
	}
	afterCount, _ := m.stashIndexAfter()
	return stashMsg, stashErr == nil && afterCount > beforeCount, nil
}

// addWorktree runs `git worktree add`. If the branch already exists but is not
// checked out anywhere, attach it without `-b`; otherwise create a fresh branch
// from HEAD.
func (m *WorktreeManager) addWorktree(wtPath, branch string, branchExists bool) error {
	var out string
	var err error
	if branchExists {
		out, err = m.git("worktree", "add", wtPath, branch)
	} else {
		out, err = m.git("worktree", "add", "-b", branch, wtPath, "HEAD")
	}
	if err != nil {
		return fmt.Errorf("git worktree add: %w: %s", err, out)
	}
	return nil
}

// Create creates a new git worktree for the given agent ID.
// If the current branch has uncommitted changes, they are stashed before
// creating the worktree and recorded in worktreeInfo.StashMsg so that MergeBack
// or Cleanup can restore them later (looked up by that unique message).
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
	// `git worktree add -b` — see claimExistingWorktree.
	branchExists := m.branchExists(branch)
	attachedAt := m.worktreeForBranch(branch)

	reuse, err := m.claimExistingWorktree(agentID, branch, wtPath, branchExists, attachedAt)
	if err != nil {
		return "", err
	}
	if reuse {
		m.active[agentID] = worktreeInfo{Path: wtPath, Branch: branch}
		return wtPath, nil
	}

	stashMsg, stashedSomething, err := m.stashBeforeWorktreeAdd(agentID)
	if err != nil {
		return "", err
	}

	// Everything from here to the success path below is paired with that
	// stash: the only exit between the two is the failure branch immediately
	// after `worktree add`, which restores the stash entry explicitly.
	if addErr := m.addWorktree(wtPath, branch, branchExists); addErr != nil {
		if !stashedSomething {
			return "", addErr
		}
		// Put the stashed changes back. `git stash push -u` has already
		// reverted the working tree, so at this point the user's uncommitted
		// work exists *only* as a stash entry: dropping it here (which is what
		// this branch used to do) deletes it without applying it, leaving the
		// content reachable only as a dangling commit until gc.
		if restoreErr := m.popStashByMessage(stashMsg); restoreErr != nil {
			// A failed restore is worse for the user than the failed worktree
			// add that got us here, and neither cause may be dropped: they
			// need to know why the worktree failed *and* that their work is
			// still sitting in the stash. Both are wrapped so errors.Is/As
			// still sees them.
			return "", fmt.Errorf("%w; uncommitted changes could NOT be restored and remain stashed as %q — recover them with `git stash list` and `git stash apply`: %w", addErr, stashMsg, restoreErr)
		}
		return "", addErr
	}

	// Stash survives on success — MergeBack or, failing that, Cleanup restores
	// it. Storing the message here is what makes that restore deterministic
	// even if other stash entries appear between Create and it.
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

	// Force-add everything an agent left in the worktree, ignoring ignore rules.
	// A plain `git status --porcelain` + `git add -A` silently drops planner
	// output: the real repo ignores **/specs/ (global) and .pi-go/ (project),
	// both of which match the artifacts written into a worktree
	// (specs/<task>/...). Ignored files do not show in plain `--porcelain`, so a
	// status-first gate would report "nothing to commit", skip the snapshot, and
	// then Cleanup would `worktree remove --force` the only copy. Force-adding
	// first, then checking what was actually staged, keeps the empty-commit
	// no-op intact while never dropping ignored work.
	if out, err := m.gitIn(info.Path, "add", "-Af"); err != nil {
		return false, fmt.Errorf("staging worktree changes: %w: %s", err, out)
	}
	staged, err := m.gitIn(info.Path, "diff", "--cached", "--name-only")
	if err != nil {
		return false, fmt.Errorf("worktree staged diff: %w: %s", err, staged)
	}
	if staged == "" {
		return false, nil
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
// If a stash was created during Create and not yet restored (e.g. MergeBack
// was never called), it is applied back to the working tree here and only then
// dropped; a failed restore is reported and leaves the entry in place.
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

	// Restore any stash still outstanding from Create. Reaching here with one
	// means MergeBack never ran — a spawn that failed, a shutdown mid-setup, a
	// result the caller discarded — so this is the last place that can hand
	// the user's uncommitted work back to them. Dropping it instead, which is
	// what this did, destroyed work that had nothing to do with the agent on
	// paths that are not even error paths (see "The stash lifecycle").
	//
	// It runs after the worktree and branch are gone so the checkout the stash
	// restores into is the main tree, never one git is about to remove.
	if info.StashMsg != "" {
		// errStashEntryGone is expected and benign: MergeBack already restored
		// and dropped the entry, or the user popped it themselves.
		if err := m.popStashByMessage(info.StashMsg); err != nil && !errors.Is(err, errStashEntryGone) {
			errs = append(errs, fmt.Sprintf("restore stashed changes: %v (your uncommitted work is still stashed as %q — recover it with `git stash list` and `git stash apply`)", err, info.StashMsg))
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
// On a successful merge, any stash created during Create is applied back to
// the main repo and then dropped. If that apply fails the entry is left in the
// stash list and the failure is returned, per "The stash lifecycle".
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
		// errStashEntryGone is benign here: nothing was outstanding to restore.
		if applyErr := m.popStashByMessage(info.StashMsg); applyErr != nil && !errors.Is(applyErr, errStashEntryGone) {
			// The entry stays in the stash list. Apply usually fails because
			// the merge just landed changes that collide with it, which means
			// the stash is now the only copy of the user's work — this used to
			// drop it "so the stash list stays bounded" while pointing the
			// user at `git stash show`, which by then had nothing left to
			// show. See "The stash lifecycle".
			return out, fmt.Errorf("merge succeeded but restoring your stashed changes failed: %w — they are still stashed as %q, recover them with `git stash list` and `git stash apply` (merge output: %s)", applyErr, info.StashMsg, out)
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
	if m.gitRunner != nil {
		return m.gitRunner(dir, args...)
	}
	return runGit(dir, args...)
}

// runGit shells out to git in dir and returns trimmed combined output.
func runGit(dir string, args ...string) (string, error) {
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
