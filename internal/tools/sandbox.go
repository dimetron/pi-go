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
	root *os.Root
	dir  string // absolute path of the root directory
}

// NewSandbox opens an os.Root anchored at dir.
func NewSandbox(dir string) (*Sandbox, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving sandbox dir: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("opening sandbox root %s: %w", abs, err)
	}
	return &Sandbox{root: root, dir: abs}, nil
}

// Close releases the underlying os.Root file descriptor.
func (s *Sandbox) Close() error {
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

// Resolve converts an absolute or relative path to a relative path
// under the sandbox root. Returns an error with the sandbox root path
// if the resolved path would escape the directory tree.
//
// SECURITY: This is intentional. The sandbox restricts file system
// access to prevent the agent from reading/writing files outside
// the working directory.
func (s *Sandbox) Resolve(name string) (string, error) {
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

// ReadFile reads the named file within the sandbox.
// Transient errors (e.g. "text file busy") are retried up to 3 times
// with increasing delay. Non-transient errors are returned immediately.
func (s *Sandbox) ReadFile(name string) ([]byte, error) {
	rel, err := s.Resolve(name)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for attempt := 0; attempt < maxReadRetries; attempt++ {
		data, err := s.root.ReadFile(rel)
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
	rel, err := s.Resolve(name)
	if err != nil {
		return err
	}
	dir := filepath.Dir(rel)
	if dir != "." {
		if err := s.root.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating directories: %w", err)
		}
	}
	f, err := s.root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
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
	rel, err := s.Resolve(name)
	if err != nil {
		return nil, err
	}
	return s.root.Open(rel)
}

// Stat returns FileInfo for a path within the sandbox.
func (s *Sandbox) Stat(name string) (os.FileInfo, error) {
	rel, err := s.Resolve(name)
	if err != nil {
		return nil, err
	}
	return s.root.Lstat(rel)
}

// ReadDir lists entries in a directory within the sandbox.
func (s *Sandbox) ReadDir(name string) ([]os.DirEntry, error) {
	rel, err := s.Resolve(name)
	if err != nil {
		return nil, err
	}
	f, err := s.root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// MkdirAll creates a directory path within the sandbox.
func (s *Sandbox) MkdirAll(name string, perm os.FileMode) error {
	rel, err := s.Resolve(name)
	if err != nil {
		return err
	}
	return s.root.MkdirAll(rel, perm)
}
