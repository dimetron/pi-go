package tools

import (
	"fmt"
	"strings"

	"google.golang.org/adk/tool"
)

// EditInput defines the parameters for the edit tool.
type EditInput struct {
	// The absolute path to the file to edit.
	FilePath string `json:"file_path"`
	// The exact string to find and replace.
	OldString string `json:"old_string"`
	// The replacement string.
	NewString string `json:"new_string"`
	// If true, replace all occurrences. Default: replace first occurrence only.
	ReplaceAll bool `json:"replace_all,omitempty"`
}

// EditOutput contains the result of editing a file.
type EditOutput struct {
	// The path of the edited file.
	Path string `json:"path"`
	// Number of replacements made.
	Replacements int `json:"replacements"`
}

func newEditTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("edit", `Edit a file by replacing an exact string match.

Required: file_path (absolute path), old_string (text to find), new_string (replacement).
Optional: replace_all (bool, default false). old_string must be unique unless replace_all is true.`, func(_ tool.Context, input EditInput) (EditOutput, error) {
		return editHandler(sb, input)
	})
}

func editHandler(sb *Sandbox, input EditInput) (EditOutput, error) {
	if input.FilePath == "" {
		return EditOutput{}, fmt.Errorf("file_path is required")
	}
	if input.OldString == "" {
		return EditOutput{}, fmt.Errorf("old_string is required")
	}
	if input.OldString == input.NewString {
		return EditOutput{}, fmt.Errorf("old_string and new_string must be different")
	}

	data, err := sb.ReadFile(input.FilePath)
	if err != nil {
		return EditOutput{}, fmt.Errorf("reading file: %w", err)
	}

	content := string(data)
	count := strings.Count(content, input.OldString)

	if count == 0 {
		return EditOutput{}, fmt.Errorf("old_string not found in file")
	}
	if count > 1 && !input.ReplaceAll {
		return EditOutput{}, fmt.Errorf("old_string found %d times in file; set replace_all=true to replace all occurrences, or provide more context to make the match unique", count)
	}

	var result string
	if input.ReplaceAll {
		result = strings.ReplaceAll(content, input.OldString, input.NewString)
	} else {
		result = strings.Replace(content, input.OldString, input.NewString, 1)
		count = 1
	}

	if err := sb.WriteFile(input.FilePath, []byte(result), 0o644); err != nil {
		return EditOutput{}, fmt.Errorf("writing file: %w", err)
	}

	return EditOutput{
		Path:         input.FilePath,
		Replacements: count,
	}, nil
}
