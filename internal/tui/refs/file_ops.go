package refs

import (
	"os"
)

// readFile reads a file and returns its content.
func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// readDir reads a directory and returns entries.
func readDir(path string) ([]DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var result []DirEntry
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		result = append(result, DirEntry{
			Name:  entry.Name(),
			IsDir: entry.IsDir(),
			Size:  info.Size(),
			Mode:  info.Mode().String(),
		})
	}

	return result, nil
}
