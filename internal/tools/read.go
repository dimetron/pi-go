package tools

import (
	"fmt"
	"strings"

	"google.golang.org/adk/tool"
)

const defaultReadLimit = 2000 // max lines returned when no limit specified

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
	return newTool("read", "Read a file's contents. Returns the content with line numbers. Supports optional line offset and limit for reading portions of large files.", func(_ tool.Context, input ReadInput) (ReadOutput, error) {
		return readHandler(sb, input)
	})
}

func readHandler(sb *Sandbox, input ReadInput) (ReadOutput, error) {
	if input.FilePath == "" {
		return ReadOutput{}, fmt.Errorf("file_path is required")
	}

	data, err := sb.ReadFile(input.FilePath)
	if err != nil {
		return ReadOutput{}, fmt.Errorf("reading file: %w", err)
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

	// Determine effective limit
	limit := input.Limit
	if limit <= 0 {
		limit = defaultReadLimit
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
