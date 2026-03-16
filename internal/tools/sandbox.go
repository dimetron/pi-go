package tools

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Sandbox restricts file system access to a directory tree using os.Root.
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

// Resolve converts an absolute or relative path to a relative path under the
// sandbox root. os.Root enforces that the resolved path cannot escape the
// directory tree (via ".." or symlinks).
func (s *Sandbox) Resolve(name string) (string, error) {
	if filepath.IsAbs(name) {
		rel, err := filepath.Rel(s.dir, name)
		if err != nil {
			return "", fmt.Errorf("path %s is outside sandbox %s", name, s.dir)
		}
		return rel, nil
	}
	return name, nil
}

// ReadFile reads the named file within the sandbox.
func (s *Sandbox) ReadFile(name string) ([]byte, error) {
	rel, err := s.Resolve(name)
	if err != nil {
		return nil, err
	}
	return s.root.ReadFile(rel)
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
