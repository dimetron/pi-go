package tui

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

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
		candidates = matchingSpecs(input, workDir)
	}

	// Filter out exact matches for single candidates (no ghost for exact match)
	// But keep them if there are multiple candidates
	if len(candidates) > 1 {
		filtered := make([]CompletionCandidate, 0)
		for _, c := range candidates {
			if c.Text != input {
				filtered = append(filtered, c)
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

	// Check for spec completion (/plan <arg> or /run <arg>)
	if strings.HasPrefix(input, "/plan ") || strings.HasPrefix(input, "/run ") {
		return CompletionTypeSpec
	}

	// Check for partial command or skill (starts with /, no space)
	if strings.HasPrefix(input, "/") && !strings.Contains(input, " ") {
		// Could be command or skill - we'll match both in Complete()
		return CompletionTypeCommand
	}

	return CompletionTypeNone
}

// matchingCommands returns all command candidates matching the prefix.
func matchingCommands(prefix string) []CompletionCandidate {
	prefix = strings.ToLower(prefix)

	var candidates []CompletionCandidate

	// Check against slash commands
	for _, cmd := range slashCommands {
		if strings.HasPrefix(strings.ToLower(cmd), prefix) {
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

// listSpecs scans the specs/ directory for subdirectories containing PROMPT.md.
// Returns a sorted list of spec names.
func listSpecs(workDir string) ([]string, error) {
	if workDir == "" {
		return nil, nil
	}

	specsDir := filepath.Join(workDir, "specs")

	entries, err := os.ReadDir(specsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var specs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		promptPath := filepath.Join(specsDir, entry.Name(), "PROMPT.md")
		if _, err := os.Stat(promptPath); err == nil {
			specs = append(specs, entry.Name())
		}
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
// Returns the start index of '@' and the text after it, or -1 if no mention found.
func findMentionAtCursor(text string, cursorPos int) (start int, prefix string) {
	for i := cursorPos - 1; i >= 0; i-- {
		if text[i] == '@' {
			return i, text[i+1 : cursorPos]
		}
		if text[i] == ' ' || text[i] == '\t' || text[i] == '\n' {
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
