package refs

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Constants for truncation limits.
const (
	// MaxLinesPerRef is the maximum number of lines for a single reference.
	MaxLinesPerRef = 500

	// MaxFolderEntries is the maximum number of entries in a folder listing.
	MaxFolderEntries = 200
)

// Expander processes context references in user input.
type Expander struct {
	validator  *Validator
	workDir    string
	maxLines   int
	maxEntries int
}

// NewExpander creates a new Expander with the given work directory.
func NewExpander(workDir string) *Expander {
	return &Expander{
		validator:  NewValidator(workDir),
		workDir:    workDir,
		maxLines:   MaxLinesPerRef,
		maxEntries: MaxFolderEntries,
	}
}

// ExpansionResult contains the result of expanding references.
type ExpansionResult struct {
	// Original is the original input message.
	Original string

	// Expanded is the message with reference content appended.
	Expanded string

	// RefsFound is the list of references found.
	RefsFound []ParsedRef

	// RefsExpanded maps reference raw values to their expanded content.
	RefsExpanded map[string]string

	// Warnings contains warning messages for invalid refs.
	Warnings []string

	// Truncated indicates which refs were truncated.
	Truncated []string
}

// Expand parses references in input and expands them to their content.
// Returns the expanded message and any warnings.
func (e *Expander) Expand(input string) (*ExpansionResult, error) {
	result := &ExpansionResult{
		Original:     input,
		Expanded:     input,
		RefsFound:    nil,
		RefsExpanded: make(map[string]string),
		Warnings:     nil,
		Truncated:    nil,
	}

	// Parse all references.
	refs := parseRefs(input)
	if len(refs) == 0 {
		return result, nil
	}

	result.RefsFound = refs

	// Build sections for expanded content.
	var sections []string

	for _, ref := range refs {
		content, warning := e.expandRef(ref)
		if warning != "" {
			result.Warnings = append(result.Warnings, warning)
		}
		if content != "" {
			rawKey := fmt.Sprintf("@%s:%s", ref.Type, ref.RawValue)
			if ref.Type == RefDiff || ref.Type == RefStaged {
				rawKey = fmt.Sprintf("@%s", ref.Type)
			}
			result.RefsExpanded[rawKey] = content
			sections = append(sections, content)
		}
	}

	if len(sections) > 0 {
		result.Expanded = input + "\n\n--- Attached Context ---\n\n" + strings.Join(sections, "\n\n---\n\n")
	}

	return result, nil
}

// expandRef expands a single reference and returns content and warning.
func (e *Expander) expandRef(ref ParsedRef) (string, string) {
	switch ref.Type {
	case RefFile:
		return e.expandFile(ref)
	case RefFolder:
		return e.expandFolder(ref)
	case RefDiff:
		return e.expandDiff(ref)
	case RefStaged:
		return e.expandStaged(ref)
	case RefGit:
		return e.expandGitLog(ref)
	case RefURL:
		return e.expandURL(ref)
	default:
		return "", fmt.Sprintf("unknown reference type: %s", ref.Type)
	}
}

// expandFile expands a file reference to its contents.
func (e *Expander) expandFile(ref ParsedRef) (string, string) {
	path := ref.Value
	if path == "" {
		return "", "file path is empty"
	}

	// Validate the file path.
	if err := e.validator.ValidateFile(path); err != nil {
		return "", fmt.Sprintf("file %s: %v", path, err)
	}

	// Resolve the full path.
	fullPath := path
	if !filepath.IsAbs(path) && e.workDir != "" {
		fullPath = filepath.Join(e.workDir, path)
	}

	// Read the file.
	data, err := readFile(fullPath)
	if err != nil {
		return "", fmt.Sprintf("file %s: %v", path, err)
	}

	// Check for binary content.
	if e.validator.IsBinaryFile(data) {
		return "", fmt.Sprintf("file %s: binary files are not supported", path)
	}

	// Split into lines for range selection and truncation.
	lines := strings.Split(string(data), "\n")

	// Apply line range if specified.
	start, end := 0, len(lines)-1
	if ref.LineRange != nil {
		// Convert from 1-based to 0-based indexing.
		start = ref.LineRange.Start - 1
		end = ref.LineRange.End - 1
		if start < 0 {
			start = 0
		}
		if end >= len(lines) {
			end = len(lines) - 1
		}
		if start > end {
			start = end
		}
	}

	// Apply truncation.
	truncated := false
	if end-start+1 > e.maxLines {
		end = start + e.maxLines - 1
		truncated = true
	}

	if end >= len(lines) {
		end = len(lines) - 1
	}
	if start > end {
		start = end
	}

	selectedLines := lines[start : end+1]
	content := strings.Join(selectedLines, "\n")

	// Format the output.
	var header string
	if ref.LineRange != nil {
		header = fmt.Sprintf("[Referenced file: %s:%d-%d]", path, ref.LineRange.Start, ref.LineRange.End)
	} else {
		header = fmt.Sprintf("[Referenced file: %s]", path)
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n```\n")
	sb.WriteString(content)
	sb.WriteString("\n```")

	if truncated {
		fmt.Fprintf(&sb, "\n\n[Truncated: file exceeds %d line limit]", e.maxLines)
	}

	return sb.String(), ""
}

// expandFolder expands a folder reference to a directory tree.
func (e *Expander) expandFolder(ref ParsedRef) (string, string) {
	path := ref.Value
	if path == "" {
		return "", "folder path is empty"
	}

	// Validate the folder path.
	if err := e.validator.ValidateFolder(path); err != nil {
		return "", fmt.Sprintf("folder %s: %v", path, err)
	}

	// Resolve the full path.
	fullPath := path
	if !filepath.IsAbs(path) && e.workDir != "" {
		fullPath = filepath.Join(e.workDir, path)
	}

	// Read the directory.
	entries, err := readDir(fullPath)
	if err != nil {
		return "", fmt.Sprintf("folder %s: %v", path, err)
	}

	// Sort entries.
	sort.Slice(entries, func(i, j int) bool {
		// Directories first, then alphabetically.
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})

	// Limit entries.
	truncated := false
	if len(entries) > e.maxEntries {
		entries = entries[:e.maxEntries]
		truncated = true
	}

	// Format the tree.
	var sb strings.Builder
	fmt.Fprintf(&sb, "[Referenced folder: %s]", path)
	sb.WriteString("\n```\n")

	for _, entry := range entries {
		line := formatDirEntry(path, entry)
		sb.WriteString(line)
		sb.WriteString("\n")
	}

	sb.WriteString("```")

	if truncated {
		fmt.Fprintf(&sb, "\n\n[Truncated: folder has more than %d entries, showing first %d]", e.maxEntries, e.maxEntries)
	}

	return sb.String(), ""
}

// DirEntry represents a directory entry with metadata.
type DirEntry struct {
	Name  string
	IsDir bool
	Size  int64
	Mode  string
}

// formatDirEntry formats a single directory entry for tree display.
func formatDirEntry(basePath string, entry DirEntry) string {
	relPath := filepath.Join(basePath, entry.Name)
	if entry.IsDir {
		return relPath + "/"
	}
	return fmt.Sprintf("%s (%s)", relPath, formatSize(entry.Size))
}

// formatSize formats a file size in human-readable form.
func formatSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%dB", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(size)/float64(div), "KMGTPE"[exp])
}

// expandDiff is implemented in git.go.
func (e *Expander) expandDiff(ref ParsedRef) (string, string) {
	return "", "@diff not yet implemented"
}

// expandStaged is implemented in git.go.
func (e *Expander) expandStaged(ref ParsedRef) (string, string) {
	return "", "@staged not yet implemented"
}

// expandGitLog is implemented in git.go.
func (e *Expander) expandGitLog(ref ParsedRef) (string, string) {
	return "", "@git not yet implemented"
}

// expandURL is implemented in web.go.
func (e *Expander) expandURL(ref ParsedRef) (string, string) {
	return "", "@url not yet implemented"
}

// Truncate truncates content to maxLines lines.
func Truncate(content string, maxLines int) (string, bool) {
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content, false
	}
	return strings.Join(lines[:maxLines], "\n"), true
}
