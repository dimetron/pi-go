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
	return editHandlerWithCache(sb, input, nil)
}

func editHandlerWithCache(sb *Sandbox, input EditInput, cache *fileContentCache) (EditOutput, error) {
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

	// If not found, retry once (file may have been modified concurrently)
	if count == 0 {
		data2, err2 := reReadFile(sb, input.FilePath)
		if err2 == nil {
			content = string(data2)
			count = strings.Count(content, input.OldString)
		}
	}

	if count == 0 {
		return EditOutput{}, buildEditNotFoundError(input, content)
	}
	if count > 1 && !input.ReplaceAll {
		// Find line numbers for all occurrences to help the caller
		lines := strings.Split(content, "\n")
		var locations []string
		offset := 0
		for lineNum, line := range lines {
			idx := strings.Index(line, input.OldString)
			if idx >= 0 {
				locations = append(locations, fmt.Sprintf("line %d (col %d)", lineNum+1, idx+1))
			}
			offset += len(line) + 1
		}
		return EditOutput{}, fmt.Errorf("old_string found %d times in file; set replace_all=true to replace all occurrences, or provide more context to make the match unique\nLocations: %s", count, strings.Join(locations, ", "))
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

	// Invalidate cache after successful edit
	if cache != nil {
		cache.Invalidate(input.FilePath)
	}

	return EditOutput{
		Path:         input.FilePath,
		Replacements: count,
	}, nil
}

// reReadFile re-reads a file from the sandbox (used for retry on edit miss).
func reReadFile(sb *Sandbox, path string) ([]byte, error) {
	return sb.ReadFile(path)
}

// buildEditNotFoundError returns an enhanced error with preview and suggestions.
func buildEditNotFoundError(input EditInput, content string) error {
	lines := strings.Split(content, "\n")
	// Find closest matching lines to help suggest what might be wrong
	var suggestions []string
	for i, line := range lines {
		if len(line) > 0 && len(input.OldString) > 0 {
			// Look for similar lines (same length ±5, share some characters)
			if abs(len(line)-len(input.OldString)) <= 5 {
				similarity := stringsSimilarity(line, input.OldString)
				if similarity > 0.5 {
					suggestions = append(suggestions, fmt.Sprintf("line %d: %q", i+1, truncate(line, 60)))
				}
			}
		}
	}
	var preview string
	if len(lines) > 0 {
		preview = truncate(lines[0], 500)
	}
	var suggestionStr string
	if len(suggestions) > 0 {
		suggestionStr = fmt.Sprintf("\nSimilar lines found:\n  %s\n\nSuggestions:", strings.Join(suggestions, "\n  "))
	}
	return fmt.Errorf(`old_string not found in file

Expected:
%q

File preview (first 500 chars of first line):
%s
%s
- Verify the exact text matches including whitespace and indentation
- Use the Read tool to see current file content with correct line endings
- Try a smaller, unique portion of the old_string
- Use replace_all=true if replacing a repeated pattern`,
		input.OldString, preview, suggestionStr)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// stringsSimilarity returns a score 0..1 indicating how similar two strings are.
// Uses a simple character-based Jaccard-like comparison.
func stringsSimilarity(a, b string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	setA := make(map[rune]bool)
	for _, r := range a {
		setA[r] = true
	}
	intersection := 0
	for _, r := range b {
		if setA[r] {
			intersection++
		}
	}
	union := len(setA) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
