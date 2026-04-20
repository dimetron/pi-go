package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dimetron/pi-go/internal/extension"
)

// CompletionType identifies what kind of completion to perform.
type CompletionType int

const (
	CompletionTypeNone CompletionType = iota
	CompletionTypeCommand
	CompletionTypeSkill
	CompletionTypeSpec
	CompletionTypeFile
)

// CompletionCandidate represents a single completion option.
type CompletionCandidate struct {
	Text        string
	Description string
	Type        CompletionType
}

// CompleteResult holds all completion results.
type CompleteResult struct {
	Candidates []CompletionCandidate
	Selected   int
	Type       CompletionType
}

// Complete returns completion candidates for the given input.
// It analyzes the input and returns all matching options for commands, skills, and specs.
func Complete(input string, skills []extension.Skill, workDir string) *CompleteResult {
	if input == "" {
		return &CompleteResult{}
	}

	// "/" alone returns no completion candidates (handled by showCommandList)
	if input == "/" {
		return &CompleteResult{}
	}

	// Determine completion type and get candidates
	var candidates []CompletionCandidate

	completionType := detectCompletionType(input)

	switch completionType {
	case CompletionTypeCommand:
		// For command completion, include both built-in commands and skills
		candidates = append(candidates, matchingCommands(input)...)
		candidates = append(candidates, matchingSkills(input, skills)...)
	case CompletionTypeSkill:
		candidates = matchingSkills(input, skills)
	case CompletionTypeSpec:
		// For /run <arg> and /plan <arg>, show spec completions.
		candidates = matchingSpecs(input, workDir)
	}

	// For /plan <arg>, also include command completions like /plan resume
	// (both spec and command completions are valid after "/plan ")
	if strings.HasPrefix(input, "/plan ") {
		candidates = append(candidates, matchingCommands(input)...)
	}

	// Deduplicate by Text (specs and commands may overlap).
	seen := make(map[string]bool)
	deduped := make([]CompletionCandidate, 0, len(candidates))
	for _, c := range candidates {
		if !seen[c.Text] {
			seen[c.Text] = true
			deduped = append(deduped, c)
		}
	}
	candidates = deduped

	// Filter out exact matches for command completion only when there are multiple candidates.
	// If user types a full command like "/skills", don't offer it as ghost when there are
	// alternatives. But if it's the only match, keep it so Tab confirms the input.
	if completionType == CompletionTypeCommand && len(candidates) > 1 {
		filtered := make([]CompletionCandidate, 0)
		for _, c := range candidates {
			// Remove exact matches
			if c.Text != input {
				// Remove candidates shorter than input (e.g., "/plan" when input is "/plan ")
				if len(c.Text) >= len(input) {
					filtered = append(filtered, c)
				}
			}
		}
		candidates = filtered
	}

	// Sort candidates alphabetically by text
	sort.Slice(candidates, func(i, j int) bool {
		return strings.ToLower(candidates[i].Text) < strings.ToLower(candidates[j].Text)
	})

	return &CompleteResult{
		Candidates: candidates,
		Selected:   0,
		Type:       completionType,
	}
}

// detectCompletionType determines what kind of completion to perform.
func detectCompletionType(input string) CompletionType {
	// Check for command completion (just /)
	if input == "/" {
		return CompletionTypeCommand
	}

	// Check for spec completion.
	// Both /run <arg> and /plan <arg> complete from specs directory.
	// NOTE: /plan resume is handled as command completion (matched by /plan r).
	if strings.HasPrefix(input, "/run ") || strings.HasPrefix(input, "/plan ") {
		return CompletionTypeSpec
	}

	// Check for partial command or skill (starts with /, no space)
	if strings.HasPrefix(input, "/") && !strings.Contains(input, " ") {
		// Could be command or skill - we'll match both in Complete()
		return CompletionTypeCommand
	}

	// Input has a space after / - check if it's a command with arguments (e.g., "/plan ")
	// These should still be matched as commands (e.g., "/plan " matches "/plan resume")
	if strings.HasPrefix(input, "/") {
		return CompletionTypeCommand
	}

	return CompletionTypeNone
}

// matchingCommands returns all command candidates matching the prefix.
func matchingCommands(prefix string) []CompletionCandidate {
	prefixLower := strings.ToLower(prefix)

	var candidates []CompletionCandidate

	// Check against slash commands
	for _, cmd := range slashCommands {
		cmdLower := strings.ToLower(cmd)
		// Match if command starts with prefix
		if strings.HasPrefix(cmdLower, prefixLower) {
			// If prefix has a trailing space (e.g., "/plan "), only match commands
			// that have more content after that space (e.g., "/plan resume")
			// Don't match "/plan" alone since that's just the command name
			if strings.HasSuffix(prefix, " ") {
				// Check if there's actually something after the space in the command
				// e.g., prefix="/plan " should match "/plan resume" but not "/plan"
				afterPrefix := cmdLower[len(prefixLower):]
				if afterPrefix == "" {
					// Command ends at the space (e.g., "/plan"), skip it
					continue
				}
			}
			desc := slashCommandDesc(cmd)
			candidates = append(candidates, CompletionCandidate{
				Text:        cmd,
				Description: desc,
				Type:        CompletionTypeCommand,
			})
		}
	}

	return candidates
}

// matchingSkills returns all skill candidates matching the prefix.
func matchingSkills(prefix string, skills []extension.Skill) []CompletionCandidate {
	prefix = strings.ToLower(strings.TrimPrefix(prefix, "/"))

	var candidates []CompletionCandidate

	for _, skill := range skills {
		if strings.HasPrefix(strings.ToLower(skill.Name), prefix) {
			candidates = append(candidates, CompletionCandidate{
				Text:        "/" + skill.Name,
				Description: skill.Description,
				Type:        CompletionTypeSkill,
			})
		}
	}

	return candidates
}

// matchingSpecs returns all spec candidates matching the prefix from the specs directory.
// It scans for subdirectories in specs/ that contain PROMPT.md.
func matchingSpecs(input string, workDir string) []CompletionCandidate {
	// Extract the argument after /plan or /run
	var argPrefix string
	if strings.HasPrefix(input, "/plan ") {
		argPrefix = strings.TrimPrefix(input, "/plan ")
	} else if strings.HasPrefix(input, "/run ") {
		argPrefix = strings.TrimPrefix(input, "/run ")
	}

	specs, err := listSpecs(workDir)
	if err != nil {
		return nil
	}

	var candidates []CompletionCandidate
	for _, spec := range specs {
		if strings.HasPrefix(strings.ToLower(spec), strings.ToLower(argPrefix)) {
			// Determine which command to complete based on input prefix
			cmdPrefix := "/plan "
			if strings.HasPrefix(input, "/run ") {
				cmdPrefix = "/run "
			}
			candidates = append(candidates, CompletionCandidate{
				Text:        cmdPrefix + spec,
				Description: "spec: " + spec,
				Type:        CompletionTypeSpec,
			})
		}
	}

	return candidates
}

// listSpecs scans the specs/ directory (including nested subdirectories) for
// subdirectories containing PROMPT.md. Returns a sorted list of spec names.
// Nested specs use relative paths (e.g. "tools/001-a2a-client").
func listSpecs(workDir string) ([]string, error) {
	if workDir == "" {
		return nil, nil
	}

	specsDir := filepath.Join(workDir, "specs")

	var specs []string
	err := filepath.WalkDir(specsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		// Only consider directories that contain PROMPT.md
		if !d.IsDir() {
			return nil
		}
		promptPath := filepath.Join(path, "PROMPT.md")
		if _, err := os.Stat(promptPath); err != nil {
			return nil
		}
		// Compute the relative path from specsDir
		rel, err := filepath.Rel(specsDir, path)
		if err != nil {
			return nil
		}
		specs = append(specs, rel)
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	sort.Strings(specs)
	return specs, nil
}

// CompleteMention returns file completion candidates for the given prefix.
func CompleteMention(prefix string, workDir string) *CompleteResult {
	candidates := matchingFiles(prefix, workDir)
	return &CompleteResult{
		Candidates: candidates,
		Selected:   0,
		Type:       CompletionTypeFile,
	}
}

// matchingFiles returns files in workDir whose relative path starts with the prefix.
// Skips hidden directories, node_modules, vendor, and binary artifacts.
// Returns at most 20 candidates.
func matchingFiles(prefix string, workDir string) []CompletionCandidate {
	if workDir == "" {
		return nil
	}

	lowerPrefix := strings.ToLower(prefix)
	var candidates []CompletionCandidate

	_ = filepath.WalkDir(workDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(workDir, path)
		if rel == "." {
			return nil
		}

		base := d.Name()
		if strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		lowerRel := strings.ToLower(rel)
		if strings.HasPrefix(lowerRel, lowerPrefix) || (lowerPrefix != "" && fuzzyMatchPath(lowerRel, lowerPrefix)) {
			candidates = append(candidates, CompletionCandidate{
				Text:        rel,
				Description: "file",
				Type:        CompletionTypeFile,
			})
		}

		if len(candidates) >= 20 {
			return filepath.SkipAll
		}
		return nil
	})

	sort.Slice(candidates, func(i, j int) bool {
		return strings.ToLower(candidates[i].Text) < strings.ToLower(candidates[j].Text)
	})

	return candidates
}

// fuzzyMatchPath checks if all parts of the query appear in order in the path.
func fuzzyMatchPath(path, query string) bool {
	pi := 0
	for qi := 0; qi < len(query) && pi < len(path); qi++ {
		idx := strings.IndexByte(path[pi:], query[qi])
		if idx < 0 {
			return false
		}
		pi += idx + 1
	}
	return pi <= len(path)
}

// findMentionAtCursor finds the @mention prefix at the cursor position.
// cursorPos is a character position. Returns the character position of '@' and the prefix after it, or -1 if no mention found.
func findMentionAtCursor(text string, cursorPos int) (start int, prefix string) {
	for i := cursorPos - 1; i >= 0; i-- {
		charByteIdx := charOffsetToByteOffset(text, i)
		if text[charByteIdx] == '@' {
			prefixByteStart := charOffsetToByteOffset(text, i+1)
			return i, text[prefixByteStart:charOffsetToByteOffset(text, cursorPos)]
		}
		charByteIdx = charOffsetToByteOffset(text, i)
		r, _ := utf8.DecodeRuneInString(text[charByteIdx:])
		if r == ' ' || r == '\t' || r == '\n' {
			break
		}
	}
	return -1, ""
}

// extractMentions finds all @path mentions in text and returns their paths.
func extractMentions(text string) []string {
	var mentions []string
	for i := 0; i < len(text); i++ {
		if text[i] != '@' {
			continue
		}
		// Extract the path after @
		j := i + 1
		for j < len(text) && text[j] != ' ' && text[j] != '\t' && text[j] != '\n' && text[j] != '@' {
			j++
		}
		if j > i+1 {
			mentions = append(mentions, text[i+1:j])
		}
		i = j - 1
	}
	return mentions
}

// CycleSelection moves the selection index in the given direction.
// dir should be 1 for next, -1 for previous.
func (r *CompleteResult) CycleSelection(dir int) {
	if len(r.Candidates) == 0 {
		return
	}
	r.Selected = (r.Selected + dir + len(r.Candidates)) % len(r.Candidates)
}

// ApplySelection returns the text of the candidate at the given index.
func (r *CompleteResult) ApplySelection(index int) string {
	if index < 0 || index >= len(r.Candidates) {
		return ""
	}
	return r.Candidates[index].Text
}

// SelectedCandidate returns the currently selected candidate.
func (r *CompleteResult) SelectedCandidate() *CompletionCandidate {
	if r.Selected < 0 || r.Selected >= len(r.Candidates) {
		return nil
	}
	return &r.Candidates[r.Selected]
}
