package tools

import (
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
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

func newWriteTool(sb *Sandbox, ledger *ReadLedger) (tool.Tool, error) {
	return newTool("write", `Write content to a file. Creates parent directories if needed. Overwrites existing files.

An existing file must have been read in full first, so the content being replaced is known.

Required: file_path (absolute path), content (file content to write).`, func(_ agent.Context, input WriteInput) (WriteOutput, error) {
		return writeHandlerWithLedger(sb, input, ledger)
	})
}

func writeHandler(sb *Sandbox, input WriteInput) (WriteOutput, error) {
	return writeHandlerWithLedger(sb, input, nil)
}

func writeHandlerWithLedger(sb *Sandbox, input WriteInput, ledger *ReadLedger) (WriteOutput, error) {
	if input.FilePath == "" {
		return WriteOutput{}, fmt.Errorf("file_path is required")
	}

	// Creating a file is always allowed; only replacing existing content needs
	// the agent to know what it is replacing.
	if info, statErr := sb.Stat(input.FilePath); statErr == nil && !info.IsDir() {
		if err := ledger.CheckOverwrite(input.FilePath, info); err != nil {
			return WriteOutput{}, err
		}
	}

	if err := sb.WriteFile(input.FilePath, []byte(input.Content), 0o644); err != nil {
		return WriteOutput{}, fmt.Errorf("writing file: %w", err)
	}

	// The file the ledger knew about no longer exists; a later overwrite must
	// be judged on a fresh read of the new content.
	ledger.Forget(input.FilePath)

	return WriteOutput{
		Path:         input.FilePath,
		BytesWritten: len(input.Content),
	}, nil
}
