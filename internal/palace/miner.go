package palace

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Default chunk size for project mining.
const defaultChunkSize = 1500

// Default overlap between chunks.
const defaultChunkOverlap = 200

// ProgressFunc is called after each file is processed during mining.
// file is the relative path, added/skipped/errors are counts for that file.
type ProgressFunc func(file string, added, skipped, errors int)

// MineConfig is the per-project configuration loaded from mempalace.yaml.
type MineConfig struct {
	Wing     string       `yaml:"wing"`
	Rooms    []RoomDef    `yaml:"rooms"`
	Progress ProgressFunc `yaml:"-"`
}

// RoomDef defines a room with glob patterns and optional keywords for
// content-based room detection.
type RoomDef struct {
	Name     string   `yaml:"name"`
	Patterns []string `yaml:"patterns,omitempty"`
	Keywords []string `yaml:"keywords,omitempty"`
}

// MineResult tracks the outcome of a mining operation.
type MineResult struct {
	Added     int
	Skipped   int // duplicates
	Processed int
	Errors    int
}

// supportedExtensions lists file extensions eligible for project mining.
var supportedExtensions = map[string]bool{
	".go":    true,
	".py":    true,
	".js":    true,
	".ts":    true,
	".tsx":   true,
	".jsx":   true,
	".rs":    true,
	".java":  true,
	".kt":    true,
	".rb":    true,
	".c":     true,
	".h":     true,
	".cpp":   true,
	".hpp":   true,
	".cs":    true,
	".swift": true,
	".md":    true,
	".txt":   true,
	".yaml":  true,
	".yml":   true,
	".toml":  true,
	".json":  true,
	".sql":   true,
	".sh":    true,
	".bash":  true,
	".zsh":   true,
	".proto": true,
}

// skipDirNames are directory names always skipped during mining.
var skipDirNames = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	".git":         true,
	".pi-go":       true,
	"__pycache__":  true,
	"dist":         true,
	"build":        true,
	".idea":        true,
	".vscode":      true,
	".next":        true,
	".cache":       true,
	"target":       true,
	"bin":          true,
	".ralph":       true,
	".agents":      true,
}

// chunkText splits text into overlapping chunks, splitting on paragraph or
// line boundaries. Each chunk is at least minSize characters.
func chunkText(text string, size, overlap int) []string {
	if len(text) <= size {
		return []string{text}
	}

	var chunks []string
	start := 0
	for start < len(text) {
		end := start + size
		if end >= len(text) {
			end = len(text)
		} else {
			// Try to split on a paragraph boundary, but only if the
			// resulting chunk would be at least half the target size.
			if idx := strings.LastIndex(text[start:end], "\n\n"); idx > size/2 {
				end = start + idx + 2
			} else if idx := strings.LastIndex(text[start:end], "\n"); idx > size/2 {
				end = start + idx + 1
			}
		}

		chunk := strings.TrimSpace(text[start:end])
		if len(chunk) >= 50 {
			chunks = append(chunks, chunk)
		}

		if end >= len(text) {
			break
		}

		// Advance past the current chunk, applying overlap.
		next := end - overlap
		if next <= start {
			next = end // always make forward progress
		}
		start = next
	}

	return chunks
}

// detectRoom maps a file path to a room name using the configured rooms.
// It checks folder paths first, then filename, then keyword scoring,
// falling back to "general".
func detectRoom(filePath string, rooms []RoomDef) string {
	rel := filepath.ToSlash(filePath)

	// Check glob patterns.
	for _, room := range rooms {
		for _, pattern := range room.Patterns {
			matched, err := filepath.Match(pattern, rel)
			if err == nil && matched {
				return room.Name
			}
			// Also try matching against just the directory prefix.
			dir := filepath.Dir(rel)
			if matched, err := filepath.Match(pattern, dir+"/"); err == nil && matched {
				return room.Name
			}
		}
	}

	// Check if the file is inside a directory matching a room name.
	parts := strings.Split(rel, "/")
	for _, room := range rooms {
		for _, part := range parts {
			if strings.EqualFold(part, room.Name) {
				return room.Name
			}
		}
	}

	// First directory component as fallback.
	if len(parts) > 1 && parts[0] != "" {
		return parts[0]
	}

	return "general"
}

// readMempalaceYAML loads a MineConfig from a mempalace.yaml file.
func readMempalaceYAML(dir string) (*MineConfig, error) {
	path := filepath.Join(dir, "mempalace.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading mempalace.yaml: %w", err)
	}

	var cfg MineConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing mempalace.yaml: %w", err)
	}

	return &cfg, nil
}

// loadGitignore parses a .gitignore file and returns patterns as a slice.
func loadGitignore(dir string) []string {
	path := filepath.Join(dir, ".gitignore")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}

// isGitignored checks whether a relative path matches any of the gitignore patterns.
// This is a simplified check — not a full gitignore implementation.
func isGitignored(relPath string, patterns []string) bool {
	for _, p := range patterns {
		p = strings.TrimSuffix(p, "/")
		// Check if any path component matches the pattern.
		parts := strings.Split(relPath, string(filepath.Separator))
		for _, part := range parts {
			if matched, _ := filepath.Match(p, part); matched {
				return true
			}
		}
		// Also check the full relative path.
		if matched, _ := filepath.Match(p, relPath); matched {
			return true
		}
	}
	return false
}
