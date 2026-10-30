package tools

import (
	"fmt"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
)

// EditInput defines the parameters for the edit tool.
type EditInput struct {
	// The absolute path to the file to edit.
	FilePath string `json:"file_path"`
	// The exact string to find and replace.
	OldString string `json:"old_string"`
	// The replacement string. Optional — empty means delete old_string.
	NewString string `json:"new_string,omitempty"`
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

func newEditTool(sb *Sandbox, ledger *ReadLedger) (tool.Tool, error) {
	return newTool("edit", `Edit a file by replacing an exact string match.

old_string must be unique unless replace_all is true. Omitting new_string deletes old_string.`, func(_ agent.Context, input EditInput) (EditOutput, error) {
		return editHandlerWithLedger(sb, input, ledger)
	})
}

func editHandler(sb *Sandbox, input EditInput) (EditOutput, error) {
	return editHandlerWithLedger(sb, input, nil)
}

// editHandlerWithLedger does not gate on the ledger: an exact-string edit
// already names the content it replaces, so requiring a prior read would be
// friction with no safety gained. It only keeps the ledger current.
func editHandlerWithLedger(sb *Sandbox, input EditInput, ledger *ReadLedger) (EditOutput, error) {
	if input.FilePath == "" {
		return EditOutput{}, fmt.Errorf("file_path is required")
	}
	if input.OldString == "" {
		return EditOutput{}, fmt.Errorf(`old_string is required

edit replaces an exact substring and needs the text to replace. A call carrying
only file_path and new_string reads like a whole-file write, which this tool
cannot do:
- to replace specific text, pass old_string as well
- to replace the entire file, use the write tool instead`)
	}
	if input.OldString == input.NewString {
		return EditOutput{}, fmt.Errorf(`old_string and new_string must be different

Both fields hold the same text, so this edit would write the file unchanged.
Change new_string to the replacement text, or drop the call`)
	}

	var content string
	var data []byte

	// Retry loop for handling concurrent modifications
	for attempt := 0; attempt < maxEditRetries; attempt++ {
		var err error
		data, err = sb.ReadFile(input.FilePath)
		if err != nil {
			return EditOutput{}, fmt.Errorf("reading file: %w", err)
		}

		content = string(data)
		count := strings.Count(content, input.OldString)

		if count > 0 {
			// Found the target string, proceed with edit
			return performEdit(sb, ledger, input, content, count)
		}

		// Not found - this might be a race condition with concurrent modification
		// Retry with a small delay
		if attempt < maxEditRetries-1 {
			time.Sleep(time.Duration(editRetryDelay*(attempt+1)) * time.Millisecond)
		}
	}

	// All retries exhausted - build helpful error message
	return EditOutput{}, buildEditNotFoundError(input, content)
}

// performEdit does the actual string replacement and file write.
func performEdit(sb *Sandbox, ledger *ReadLedger, input EditInput, content string, count int) (EditOutput, error) {
	if count > 1 && !input.ReplaceAll {
		// Find line numbers for all occurrences to help the caller
		lines := strings.Split(content, "\n")
		var locations []string
		for lineNum, line := range lines {
			idx := strings.Index(line, input.OldString)
			if idx >= 0 {
				locations = append(locations, fmt.Sprintf("line %d (col %d)", lineNum+1, idx+1))
			}
		}
		return EditOutput{}, fmt.Errorf("old_string found %d times in file; set replace_all=true to replace all occurrences, or provide more context to make the match unique\nLocations: %s", count, strings.Join(locations, ", "))
	}

	var result string
	replacements := count
	if input.ReplaceAll {
		result = strings.ReplaceAll(content, input.OldString, input.NewString)
	} else {
		result = strings.Replace(content, input.OldString, input.NewString, 1)
		replacements = 1
	}

	if err := sb.WriteFile(input.FilePath, []byte(result), 0o644); err != nil {
		return EditOutput{}, fmt.Errorf("writing file: %w", err)
	}

	// The agent changed this file itself, so refresh the recorded stat rather
	// than letting its own edit trip the changed-on-disk check later.
	if info, statErr := sb.Stat(input.FilePath); statErr == nil {
		ledger.Touch(input.FilePath, info)
	}

	return EditOutput{
		Path:         input.FilePath,
		Replacements: replacements,
	}, nil
}

// reReadFile re-reads a file from the sandbox (used for retry on edit miss).
const (
	maxEditRetries = 3
	editRetryDelay = 100 // milliseconds between retries
)

// editContextLines is how many lines around the best candidate the not-found
// error quotes. Enough to show the caller the real indentation and shape of the
// region it meant to match, without pasting the file back into the transcript.
const editContextLines = 3

// buildEditNotFoundError builds the not-found error.
//
// Two details here are load-bearing, both learned from session logs:
//
// The old_string is echoed raw between delimiters rather than with %q. %q
// doubles every backslash, and a model that transcribed "\n" as two characters
// saw its own escaping worsen on each retry — \n became \\n became \\\n —
// chasing a match against text the error itself had corrupted.
//
// The suggestion is the file window that best resembles the old_string, not a
// single line. The previous test compared one line's length against the whole
// old_string, which for a multi-line target could never be satisfied: across
// logged failures, 39 of 70 not-found errors (56%) offered no suggestion at
// all, and no retry could succeed.
func buildEditNotFoundError(input EditInput, content string) error {
	best, bestScore := bestEditContext(content, input.OldString)

	var suggestionStr string
	if bestScore > 0 {
		suggestionStr = fmt.Sprintf("Closest match in the file: line %d (similarity %.2f).\n", best.line, bestScore)
	}

	reason := editMismatchHint(input.OldString, content)

	return fmt.Errorf(`old_string not found in file

%s

old_string sent, verbatim between the markers:
<<<OLD_STRING
%s
OLD_STRING>>>

%d-line window of the file nearest that text, verbatim:
<<<FILE_WINDOW
%s
FILE_WINDOW>>>
%s
- Copy the text from a read of the file rather than retyping it
- Verify indentation matches exactly: tabs against spaces is the commonest miss
- Try a smaller, unique portion of the old_string`,
		reason, input.OldString, editContextLines, best.text, suggestionStr)
}

// editMismatchHint names the specific character-level difference between the
// old_string and the file when it is cheap and unambiguous to say so. A caller
// that is one tab or one whitespace run away from a match should be told that,
// rather than told to re-read a file it already read correctly.
func editMismatchHint(oldString, content string) string {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	if oldString != "" && strings.Contains(norm(content), norm(oldString)) {
		return "Diagnosis: the text differs from the file only in whitespace (tabs, spaces, or line wrapping)."
	}
	if strings.Contains(oldString, `\n`) || strings.Contains(oldString, `\t`) {
		return `Diagnosis: old_string contains literal backslash-n or backslash-t sequences. Real newlines and tabs belong in old_string unescaped.`
	}
	return "Diagnosis: the text is not present in the file, in whole or in part."
}

// editContext is the best-matching window of the file for a failed old_string.
type editContext struct {
	line  int // 1-based line the window starts on; 0 when nothing matched
	score float64
	text  string
}

// bestEditContext finds the file window most similar to oldString.
//
// Similarity is the character set overlap between the window and the old_string
// (see stringsSimilarity), so it is insensitive to length and survives the
// whitespace and transcription drift that causes most misses. A window
// containing newlines is scored because that is the shape real targets have;
// scoring single lines only, as this once did, could never represent one.
func bestEditContext(content, oldString string) (best editContext, bestScore float64) {
	if oldString == "" {
		return editContext{}, 0
	}
	lines := strings.Split(content, "\n")
	for i := range lines {
		end := min(i+editContextLines, len(lines))
		window := strings.Join(lines[i:end], "\n")
		if strings.TrimSpace(window) == "" {
			continue
		}
		score := stringsSimilarity(window, oldString)
		if score > bestScore {
			bestScore = score
			best = editContext{line: i + 1, score: score, text: window}
		}
	}
	return best, bestScore
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
// Uses a character-based Jaccard comparison over distinct runes.
//
// Both sets are deduplicated before the intersection is counted. The earlier
// version counted a rune once per occurrence in b while measuring the union in
// bytes, which let the result exceed 1 — a caller comparing against a threshold
// could not tell a near miss from an exact match.
func stringsSimilarity(a, b string) float64 {
	setA := make(map[rune]struct{})
	for _, r := range a {
		setA[r] = struct{}{}
	}
	setB := make(map[rune]struct{})
	for _, r := range b {
		setB[r] = struct{}{}
	}
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}
	intersection := 0
	for r := range setB {
		if _, ok := setA[r]; ok {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
