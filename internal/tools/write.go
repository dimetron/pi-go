package tools

import (
	"fmt"

	"google.golang.org/adk/tool"
)

// WriteInput defines the parameters for the write tool.
type WriteInput struct {
	// The absolute path to the file to write.
	FilePath string `json:"file_path"`
	// The content to write to the file.
	Content string `json:"content"`
}

// WriteOutput contains the result of writing a file.
type WriteOutput struct {
	// The path of the written file.
	Path string `json:"path"`
	// Number of bytes written.
	BytesWritten int `json:"bytes_written"`
}

func newWriteTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("write", "Write content to a file. Creates the file and any parent directories if they don't exist. Overwrites the file if it already exists.", func(_ tool.Context, input WriteInput) (WriteOutput, error) {
		return writeHandler(sb, input)
	})
}

func writeHandler(sb *Sandbox, input WriteInput) (WriteOutput, error) {
	if input.FilePath == "" {
		return WriteOutput{}, fmt.Errorf("file_path is required")
	}

	if err := sb.WriteFile(input.FilePath, []byte(input.Content), 0o644); err != nil {
		return WriteOutput{}, fmt.Errorf("writing file: %w", err)
	}

	return WriteOutput{
		Path:         input.FilePath,
		BytesWritten: len(input.Content),
	}, nil
}
