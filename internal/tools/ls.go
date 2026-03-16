package tools

import (
	"fmt"
	"path/filepath"

	"google.golang.org/adk/tool"
)

const maxLsEntries = 1000

// LsInput defines the parameters for the ls tool.
type LsInput struct {
	// The directory path to list. Defaults to current directory.
	Path string `json:"path,omitempty"`
}

// LsOutput contains the directory listing.
type LsOutput struct {
	// List of entries in the directory.
	Entries []LsEntry `json:"entries"`
	// Total entries in the directory (may be more than returned if truncated).
	TotalEntries int `json:"total_entries"`
	// Whether results were truncated due to limits.
	Truncated bool `json:"truncated,omitempty"`
}

// LsEntry represents a single directory entry.
type LsEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

func newLsTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("ls", "List the contents of a directory. Returns file names, types (file/directory), and sizes.", func(_ tool.Context, input LsInput) (LsOutput, error) {
		return lsHandler(sb, input)
	})
}

func lsHandler(sb *Sandbox, input LsInput) (LsOutput, error) {
	dir := input.Path
	if dir == "" {
		dir = "."
	}

	entries, err := sb.ReadDir(dir)
	if err != nil {
		return LsOutput{}, fmt.Errorf("reading directory: %w", err)
	}

	total := len(entries)
	if len(entries) > maxLsEntries {
		entries = entries[:maxLsEntries]
	}

	result := make([]LsEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, LsEntry{
			Name:  filepath.Join(dir, e.Name()),
			IsDir: e.IsDir(),
			Size:  info.Size(),
		})
	}

	return LsOutput{
		Entries:      result,
		TotalEntries: total,
		Truncated:    total > len(result),
	}, nil
}
