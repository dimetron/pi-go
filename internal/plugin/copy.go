package plugin

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// sortStrings sorts in place. Wrapping sort.Strings keeps the ordering used for
// every user-facing list in one place.
func sortStrings(s []string) { sort.Strings(s) }

// copyTree copies the directory tree at src to dst, preserving file modes.
// It is the fallback for os.Rename failing across filesystems, which is what
// happens when a plugin is installed from a local directory on another volume.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		switch {
		case d.IsDir():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())

		case d.Type()&fs.ModeSymlink != 0:
			// Reproduce the link rather than following it: a plugin may
			// deliberately link to a shared file, and following it could
			// copy data from outside the plugin entirely.
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
				return err
			}
			return os.Symlink(link, target)

		case d.Type().IsRegular():
			return copyFile(path, target, d)

		default:
			// Skip sockets, devices, and other special files.
			return nil
		}
	})
}

// copyFile copies one regular file, preserving its permission bits.
func copyFile(src, dst string, d fs.DirEntry) error {
	info, err := d.Info()
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copying %s: %w", src, err)
	}
	return out.Close()
}
