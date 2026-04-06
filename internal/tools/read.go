package tools

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"google.golang.org/adk/tool"
)

const defaultReadLimit = 2000 // max lines returned when no limit specified

// sourceCodeExts lists file extensions for which the default line limit is
// skipped so that source files are returned in full (the 256KB byte safety
// net still applies).
var sourceCodeExts = map[string]bool{
	".go":    true,
	".rs":    true,
	".py":    true,
	".ts":    true,
	".tsx":   true,
	".js":    true,
	".jsx":   true,
	".java":  true,
	".c":     true,
	".cpp":   true,
	".h":     true,
	".hpp":   true,
	".cs":    true,
	".rb":    true,
	".swift": true,
	".kt":    true,
	".scala": true,
	".zig":   true,
	".lua":   true,
	".sh":    true,
	".sql":   true,
}

// base64ImagePattern matches markdown image references with base64 data URIs
// e.g., ![Alt](data:image/png;base64,iVBORw0KG...)
var base64ImagePattern = regexp.MustCompile(`!\[([^\]]*)\]\(data:[^)]+\)`)

// stripBase64Images replaces markdown image references with base64 data URIs
// with a placeholder to reduce output size. This helps prevent LLM confusion
// when reading markdown files that contain embedded screenshots.
func stripBase64Images(content string) string {
	return base64ImagePattern.ReplaceAllString(content, "![$1](data:image/png;base64,...[stripped])")
}

// ReadInput defines the parameters for the read tool.
type ReadInput struct {
	// The absolute path to the file to read.
	FilePath string `json:"file_path"`
	// Optional line offset to start reading from (1-based). Defaults to 1.
	Offset int `json:"offset,omitempty"`
	// Optional maximum number of lines to read. 0 means up to 2000 lines.
	Limit int `json:"limit,omitempty"`
}

// ReadOutput contains the result of reading a file.
type ReadOutput struct {
	// The file content with line numbers.
	Content string `json:"content"`
	// Total number of lines in the file.
	TotalLines int `json:"total_lines"`
	// Whether the output was truncated.
	Truncated bool `json:"truncated,omitempty"`
}

func newReadTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("read", `Read a file's contents. Returns the content with line numbers.

Required: file_path (absolute path to the file).
Optional: offset (start line, 1-based), limit (max lines to read).`, func(_ tool.Context, input ReadInput) (ReadOutput, error) {
		return readHandler(sb, input)
	}, map[string]string{"path": "file_path"})
}

func readHandler(sb *Sandbox, input ReadInput) (ReadOutput, error) {
	return readHandlerWithCache(sb, input, nil)
}

func readHandlerWithCache(sb *Sandbox, input ReadInput, cache *FileContentCache) (ReadOutput, error) {
	if input.FilePath == "" {
		return ReadOutput{}, fmt.Errorf("file_path is required")
	}

	var data []byte

	// Try cache first (if available)
	if cache != nil {
		info, err := sb.Stat(input.FilePath)
		if err != nil {
			return ReadOutput{}, fmt.Errorf("reading file: %w", err)
		}
		mtime := info.ModTime().UnixNano()

		if cached := cache.Get(input.FilePath, mtime); cached != nil {
			data = cached
		} else {
			raw, err := sb.ReadFile(input.FilePath)
			if err != nil {
				return ReadOutput{}, fmt.Errorf("reading file: %w", err)
			}
			data = raw
			cache.Put(input.FilePath, data, mtime)
		}
	} else {
		raw, err := sb.ReadFile(input.FilePath)
		if err != nil {
			return ReadOutput{}, fmt.Errorf("reading file: %w", err)
		}
		data = raw
	}

	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	// Apply offset (1-based)
	offset := input.Offset
	if offset < 1 {
		offset = 1
	}
	if offset > totalLines {
		return ReadOutput{Content: "", TotalLines: totalLines}, nil
	}

	startIdx := offset - 1

	// Determine effective limit.
	// For source code files, skip the default line limit so the full file
	// is returned (the 256KB byte safety net still applies).
	limit := input.Limit
	if limit <= 0 {
		ext := strings.ToLower(filepath.Ext(input.FilePath))
		if sourceCodeExts[ext] {
			limit = totalLines // no line truncation for source code
		} else {
			limit = defaultReadLimit
		}
	}

	endIdx := startIdx + limit
	if endIdx > totalLines {
		endIdx = totalLines
	}
	truncated := endIdx < totalLines && input.Limit <= 0

	// Format with line numbers
	var sb2 strings.Builder
	for i := startIdx; i < endIdx; i++ {
		fmt.Fprintf(&sb2, "%6d\t%s\n", i+1, lines[i])
	}

	content := sb2.String()
	// Strip base64 images from markdown files to reduce output size
	content = stripBase64Images(content)
	if truncated {
		content += fmt.Sprintf("\n... (truncated: showing %d of %d lines, use offset/limit to read more)", endIdx-startIdx, totalLines)
	}

	// Safety net: also apply byte-level truncation
	content = truncateOutput(content)

	return ReadOutput{
		Content:    content,
		TotalLines: totalLines,
		Truncated:  truncated,
	}, nil
}
