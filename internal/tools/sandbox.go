package tools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	maxReadRetries = 3
	readRetryDelay = 50 * time.Millisecond
)

// isTransientReadError returns true for errors that might succeed on retry.
func isTransientReadError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	transient := []string{
		"text file busy",
		"resource temporarily unavailable",
		"input/output error",
	}
	for _, t := range transient {
		if strings.Contains(msg, t) {
			return true
		}
	}
	return errors.Is(err, syscall.ETIMEDOUT)
}

// Sandbox provides a secure file system abstraction that restricts
// all file operations to a specific directory tree.
//
// SECURITY MODEL:
//   - All file paths are resolved relative to the sandbox root
//   - Access outside the sandbox is blocked via os.Root (Go 1.24+)
//   - This prevents the agent from accessing sensitive files outside
//     the working directory
//
// LIMITATIONS:
//   - Files outside the sandbox cannot be accessed
//   - Symlinks pointing outside are blocked
//   - Absolute paths are converted to relative
//
// WORKAROUNDS:
//   - Change the working directory to access different files
//   - Use tools that explicitly access external resources (e.g., fetch URLs)
type Sandbox struct {
	root        *os.Root
	dir         string // absolute path of the root directory
	worktreeDir string // absolute path of worktree (if subagent context)
	extraRoots  []*os.Root
	extraDirs   []string // absolute paths of extra allowed directories
}

// NewSandbox opens an os.Root anchored at dir.
// Optionally pass worktreeDir if this is a subagent sandbox that needs to
// resolve relative paths from a different working directory.
func NewSandbox(dir string, worktreeDir ...string) (*Sandbox, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving sandbox dir: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("opening sandbox root %s: %w", abs, err)
	}
	wt := ""
	if len(worktreeDir) > 0 && worktreeDir[0] != "" {
		wt, err = filepath.Abs(worktreeDir[0])
		if err != nil {
			return nil, fmt.Errorf("resolving worktree dir: %w", err)
		}
	}
	return &Sandbox{root: root, dir: abs, worktreeDir: wt}, nil
}

// AddExtraDir registers an additional directory that the sandbox can access.
// Paths under this directory are resolved using a separate os.Root.
func (s *Sandbox) AddExtraDir(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving extra dir: %w", err)
	}
	// Create the directory if it doesn't exist.
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return fmt.Errorf("creating extra dir %s: %w", abs, err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return fmt.Errorf("opening extra root %s: %w", abs, err)
	}
	s.extraRoots = append(s.extraRoots, root)
	s.extraDirs = append(s.extraDirs, abs)
	return nil
}

// matchExtraRoot returns the extra os.Root and the relative path if the
// absolute path falls under one of the extra allowed directories.
// Returns nil, "" if no match.
func (s *Sandbox) matchExtraRoot(absPath string) (*os.Root, string) {
	for i, dir := range s.extraDirs {
		if absPath == dir || strings.HasPrefix(absPath, dir+string(filepath.Separator)) {
			rel, err := filepath.Rel(dir, absPath)
			if err != nil {
				continue
			}
			return s.extraRoots[i], rel
		}
	}
	return nil, ""
}

// Close releases the underlying os.Root file descriptor.
func (s *Sandbox) Close() error {
	for _, r := range s.extraRoots {
		_ = r.Close()
	}
	return s.root.Close()
}

// FS returns an fs.FS scoped to the sandbox root directory.
func (s *Sandbox) FS() fs.FS {
	return s.root.FS()
}

// Dir returns the absolute path of the sandbox root.
func (s *Sandbox) Dir() string {
	return s.dir
}

// SetWorktreeDir sets the worktree directory for path normalization.
// This is used by subagent sandboxes that need to resolve relative paths
// from a different working directory than the sandbox root.
func (s *Sandbox) SetWorktreeDir(dir string) error {
	if dir == "" {
		s.worktreeDir = ""
		return nil
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving worktree dir: %w", err)
	}
	s.worktreeDir = abs
	return nil
}

// Resolve converts an absolute or relative path to a relative path
// under the sandbox root. Returns an error with the sandbox root path
// if the resolved path would escape the directory tree.
//
// For subagent contexts with worktree directories, paths like "../../go.mod"
// are resolved relative to the worktree, then made relative to the sandbox root.
//
// SECURITY: This is intentional. The sandbox restricts file system
// access to prevent the agent from reading/writing files outside
// the working directory.
func (s *Sandbox) Resolve(name string) (string, error) {
	// Handle worktree-relative paths (../../ patterns from subagent worktree)
	if s.worktreeDir != "" && strings.HasPrefix(name, "../") {
		return s.resolveWorktreePath(name)
	}

	var rel string
	if filepath.IsAbs(name) {
		var err error
		rel, err = filepath.Rel(s.dir, name)
		if err != nil {
			return "", fmt.Errorf("path %s is outside sandbox root %s — use absolute paths under this directory", name, s.dir)
		}
	} else {
		rel = name
	}

	// Check if the cleaned relative path escapes the sandbox root.
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes sandbox root %s — use absolute paths starting with %s/", name, s.dir, s.dir)
	}

	return rel, nil
}

// resolveWorktreePath handles paths like "../../go.mod" from subagent worktrees.
// It converts the worktree-relative path to an absolute path, then makes it
// relative to the sandbox root while ensuring it doesn't escape.
func (s *Sandbox) resolveWorktreePath(name string) (string, error) {
	// Convert worktree-relative path to absolute
	abs, err := filepath.Abs(filepath.Join(s.worktreeDir, name))
	if err != nil {
		return "", fmt.Errorf("resolving worktree-relative path %q: %w", name, err)
	}

	// Make it relative to sandbox root
	rel, err := filepath.Rel(s.dir, abs)
	if err != nil {
		return "", fmt.Errorf("path %q resolves to %s which is outside sandbox root %s", name, abs, s.dir)
	}

	// Verify the relative path doesn't escape the sandbox
	cleaned := filepath.Clean(rel)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes sandbox root %s", name, s.dir)
	}

	return rel, nil
}

// resolveToRoot returns the os.Root and relative path for the given name.
// It first checks extra roots (for absolute paths under allowed dirs),
// then falls back to the primary sandbox root.
func (s *Sandbox) resolveToRoot(name string) (*os.Root, string, error) {
	// Check extra roots for absolute paths.
	if filepath.IsAbs(name) {
		if root, rel := s.matchExtraRoot(name); root != nil {
			return root, rel, nil
		}
	}
	// Fall back to the primary sandbox root.
	rel, err := s.Resolve(name)
	if err != nil {
		return nil, "", err
	}
	return s.root, rel, nil
}

// ReadFile reads the named file within the sandbox.
// Transient errors (e.g. "text file busy") are retried up to 3 times
// with increasing delay. Non-transient errors are returned immediately.
func (s *Sandbox) ReadFile(name string) ([]byte, error) {
	root, rel, err := s.resolveToRoot(name)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt < maxReadRetries; attempt++ {
		data, err := root.ReadFile(rel)
		if err == nil {
			return data, nil
		}

		if !isTransientReadError(err) {
			return nil, err
		}
		lastErr = err

		if attempt < maxReadRetries-1 {
			time.Sleep(readRetryDelay * time.Duration(attempt+1))
		}
	}
	return nil, lastErr
}

// WriteFile writes data to the named file within the sandbox, creating it if
// necessary (parent directories are created automatically).
func (s *Sandbox) WriteFile(name string, data []byte, perm os.FileMode) error {
	root, rel, err := s.resolveToRoot(name)
	if err != nil {
		return err
	}
	dir := filepath.Dir(rel)
	if dir != "." {
		if err := root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating directories: %w", err)
		}
	}
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// Open opens a file for reading within the sandbox.
func (s *Sandbox) Open(name string) (*os.File, error) {
	root, rel, err := s.resolveToRoot(name)
	if err != nil {
		return nil, err
	}
	return root.Open(rel)
}

// Stat returns FileInfo for a path within the sandbox.
func (s *Sandbox) Stat(name string) (os.FileInfo, error) {
	root, rel, err := s.resolveToRoot(name)
	if err != nil {
		return nil, err
	}
	return root.Lstat(rel)
}

// ReadDir lists entries in a directory within the sandbox.
func (s *Sandbox) ReadDir(name string) ([]os.DirEntry, error) {
	root, rel, err := s.resolveToRoot(name)
	if err != nil {
		return nil, err
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// MkdirAll creates a directory path within the sandbox.
func (s *Sandbox) MkdirAll(name string, perm os.FileMode) error {
	root, rel, err := s.resolveToRoot(name)
	if err != nil {
		return err
	}
	return root.MkdirAll(rel, perm)
}
