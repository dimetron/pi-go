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
// Errors are logged but do not cause failure (best-effort cleanup).
func (m *WorktreeManager) Cleanup(agentID string) error {
	m.mu.Lock()
	info, exists := m.active[agentID]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf("no worktree found for agent %s", agentID)
	}
	delete(m.active, agentID)
	m.mu.Unlock()

	var errs []string

	// Remove the worktree.
	if out, err := m.git("worktree", "remove", "--force", info.Path); err != nil {
		errs = append(errs, fmt.Sprintf("worktree remove: %v: %s", err, out))
		// Fallback: remove directory manually.
		_ = os.RemoveAll(info.Path)
	}

	// Delete the branch.
	if out, err := m.git("branch", "-D", info.Branch); err != nil {
		errs = append(errs, fmt.Sprintf("branch delete: %v: %s", err, out))
	}

	if len(errs) > 0 {
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
func (m *WorktreeManager) CleanupAll() error {
	m.mu.Lock()
	ids := make([]string, 0, len(m.active))
	for id := range m.active {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	var errs []string
	for _, id := range ids {
		if err := m.Cleanup(id); err != nil {
			errs = append(errs, fmt.Sprintf("agent %s: %v", id, err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errs, "; "))
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
