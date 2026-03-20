package subagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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
	Path   string // Filesystem path to the worktree
	Branch string // Branch name created for the worktree
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

// Create creates a new git worktree for the given agent ID.
// Returns the filesystem path to the worktree.
func (m *WorktreeManager) Create(agentID string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return "", fmt.Errorf("worktree manager is shut down")
	}

	if _, exists := m.active[agentID]; exists {
		return "", fmt.Errorf("worktree already exists for agent %s", agentID)
	}

	sid := shortID(agentID)
	branch := "pi-agent-" + sid
	wtPath := filepath.Join(m.repoRoot, ".pi-go", "worktrees", sid)

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o755); err != nil {
		return "", fmt.Errorf("creating worktree parent dir: %w", err)
	}

	// Create worktree with a new branch from HEAD.
	out, err := m.git("worktree", "add", "-b", branch, wtPath, "HEAD")
	if err != nil {
		return "", fmt.Errorf("git worktree add: %w: %s", err, out)
	}

	m.active[agentID] = worktreeInfo{Path: wtPath, Branch: branch}
	return wtPath, nil
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

	if len(errs) > 0 {
		// Re-add entry so a retry pass can attempt again.
		m.mu.Lock()
		m.active[agentID] = info
		m.mu.Unlock()
		return fmt.Errorf("cleanup errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// MergeBack merges the worktree branch back into the current branch of the main worktree.
// Returns the merge output.
func (m *WorktreeManager) MergeBack(agentID string) (string, error) {
	m.mu.Lock()
	info, exists := m.active[agentID]
	m.mu.Unlock()

	if !exists {
		return "", fmt.Errorf("no worktree found for agent %s", agentID)
	}

	out, err := m.git("merge", "--no-ff", info.Branch, "-m", fmt.Sprintf("Merge subagent %s", shortID(agentID)))
	if err != nil {
		return out, fmt.Errorf("merge failed: %w: %s", err, out)
	}
	return out, nil
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
	cmd := exec.Command("git", args...)
	cmd.Dir = m.repoRoot
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
