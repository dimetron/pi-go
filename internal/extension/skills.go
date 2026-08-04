package extension

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dimetron/pi-go/internal/audit"
)

// Skill represents a loaded skill from a SKILL.md file.
type Skill struct {
	// Name is the skill's identifier (derived from directory name).
	Name string
	// Description is a one-line description from frontmatter.
	Description string
	// Instruction is the markdown body (the system prompt to inject).
	// Empty after LoadSkills/parseSkillFile; use LoadSkillBody to read it.
	Instruction string
	// BodyPath is the on-disk path to the SKILL.md file used to read the body
	// on demand. Empty for bundled skills (their body is read from the
	// embedded filesystem via the bundledSkillsFS key).
	BodyPath string
	// Tools lists tool names this skill is allowed to use (from frontmatter).
	Tools []string
	// Source is where the skill came from: "bundled", "user", or "project".
	Source string
}

// AuditMode controls how skill loading handles audit findings.
type AuditMode string

const (
	// AuditBlock blocks skills with critical findings (default).
	AuditBlock AuditMode = "block"
	// AuditWarn loads all skills but logs warnings for critical findings.
	AuditWarn AuditMode = "warn"
	// AuditSkip skips scanning entirely (backward compat for tests).
	AuditSkip AuditMode = "skip"
)

// LoadOptions controls skill loading behavior.
type LoadOptions struct {
	AuditMode AuditMode
}

// LoadSkills discovers and loads skills from the given directories.
// It searches for <dir>/<skill-name>/SKILL.md subdirectories.
// Later directories override earlier ones (project overrides global).
// Skills with critical audit findings are blocked when AuditMode is "block" (default).
func LoadSkills(dirs ...string) ([]Skill, error) {
	return LoadSkillsWithOptions(LoadOptions{AuditMode: AuditBlock}, dirs...)
}

// LoadSkillsWithOptions discovers and loads skills with configurable audit behavior.
// Bundled skills (embedded in the binary) are loaded first and can be overridden
// by user or project skills.
func LoadSkillsWithOptions(opts LoadOptions, dirs ...string) ([]Skill, error) {
	seen := make(map[string]int) // name → index in result
	var skills []Skill
	var blocked []string

	// Load bundled skills first (lowest priority).
	bundledMap, err := LoadBundledSkills()
	if err == nil {
		for skillName, files := range bundledMap {
			var mainFile []byte
			for _, f := range files {
				if f.RelPath == "bundled_skills/"+skillName+"/SKILL.md" {
					mainFile = f.Content
					break
				}
			}
			if len(mainFile) == 0 && len(files) > 0 {
				mainFile = files[0].Content
			}
			if len(mainFile) == 0 {
				continue
			}
			skill, body, err := parseSkillContent(string(mainFile), skillName)
			if err != nil {
				continue
			}
			skill.Source = "bundled"
			skillName := skill.Name
			seen[skillName] = len(skills)
			// Body is already in memory for bundled skills — cache it so
			// LoadSkillBody doesn't need to re-read the embed fs.
			if body != "" {
				bundledBodyCache.Store(skillName, body)
			}
			skills = append(skills, skill)
		}
	}

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading skills dir %s: %w", dir, err)
		}

		skillFiles := []struct {
			path        string
			defaultName string
		}{
			{path: filepath.Join(dir, "SKILL.md"), defaultName: filepath.Base(dir)},
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillFile := filepath.Join(dir, entry.Name(), "SKILL.md")
			if _, err := os.Stat(skillFile); err != nil {
				continue
			}
			skillFiles = append(skillFiles, struct {
				path        string
				defaultName string
			}{path: skillFile, defaultName: entry.Name()})
		}

		for _, skillInfo := range skillFiles {
			if _, err := os.Stat(skillInfo.path); err != nil {
				continue
			}

			// Audit the skill file before loading.
			if opts.AuditMode != AuditSkip {
				scanResult, scanErr := audit.ScanFile(skillInfo.path)
				if scanErr != nil {
					fmt.Fprintf(os.Stderr, "pi-go: warning: audit scan failed for %s: %v\n", skillInfo.path, scanErr)
				} else if scanResult.HasCritical() {
					if opts.AuditMode == AuditBlock {
						blocked = append(blocked, skillInfo.path)
						fmt.Fprintf(os.Stderr, "pi-go: BLOCKED skill %s — critical hidden characters detected\n", skillInfo.defaultName)
						continue
					}
					// AuditWarn: log but continue loading.
					fmt.Fprintf(os.Stderr, "pi-go: WARNING: skill %s has critical hidden characters\n", skillInfo.defaultName)
				}
			}

			skill, err := parseSkillFile(skillInfo.path)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", skillInfo.path, err)
			}
			// Default name from directory if not set in frontmatter
			if skill.Name == "" {
				skill.Name = skillInfo.defaultName
			}
			// Determine source based on whether the path is absolute.
			source := "user"
			if !filepath.IsAbs(dir) {
				source = "project"
			}
			skill.Source = source
			if idx, ok := seen[skill.Name]; ok {
				// Override with project/user-level skill.
				skills[idx] = skill
			} else {
				seen[skill.Name] = len(skills)
				skills = append(skills, skill)
			}
		}
	}

	if len(blocked) > 0 {
		fmt.Fprintf(os.Stderr, "pi-go: %d skill(s) blocked due to security audit. Run 'pi audit' for details.\n", len(blocked))
	}

	return skills, nil
}

// parseSkillFile reads a SKILL.md file and returns its metadata. The body is
// not parsed at this point — use LoadSkillBody to read it on demand.
// Format:
//
//	---
//	name: skill-name
//	description: one-line description
//	tools: read, write, bash
//	---
//	Markdown instruction body...
func parseSkillFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	// Derive default name from parent directory: skills/my-skill/SKILL.md → my-skill
	name := filepath.Base(filepath.Dir(path))

	skill, _, err := parseSkillContent(string(data), name)
	if err != nil {
		return Skill{}, err
	}
	// Record the on-disk path so LoadSkillBody can read it lazily.
	skill.BodyPath = path
	return skill, nil
}

// parseSkillContent parses skill frontmatter and returns the metadata plus
// the raw body. The body is returned alongside so callers that already have
// the file contents in memory (e.g. embedded/bundled skills) can cache it
// without re-reading.
func parseSkillContent(content, skillName string) (Skill, string, error) {
	skill := Skill{Name: skillName}

	scanner := bufio.NewScanner(strings.NewReader(content))
	inFrontmatter := false
	frontmatterDone := false
	var body strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" && !frontmatterDone {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			// End of frontmatter.
			inFrontmatter = false
			frontmatterDone = true
			continue
		}

		if inFrontmatter {
			key, value, ok := parseFrontmatterLine(line)
			if !ok {
				continue
			}
			switch key {
			case "name":
				skill.Name = value
			case "description":
				skill.Description = value
			case "tools":
				for _, t := range strings.Split(value, ",") {
					t = strings.TrimSpace(t)
					if t != "" {
						skill.Tools = append(skill.Tools, t)
					}
				}
			}
		} else {
			body.WriteString(line)
			body.WriteString("\n")
		}
	}

	return skill, strings.TrimSpace(body.String()), scanner.Err()
}

// parseFrontmatterLine parses "key: value" from a frontmatter line.
func parseFrontmatterLine(line string) (key, value string, ok bool) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

// FindSkill looks up a skill by name from a slice of loaded skills.
func FindSkill(skills []Skill, name string) (Skill, bool) {
	for _, s := range skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// bundledBodyCache holds the markdown body for bundled skills, populated when
// LoadSkills parses the embed fs. Keyed by skill name; value is the body.
var bundledBodyCache sync.Map // map[string]string

// fileBodyCache holds the markdown body for file-based skills, keyed by the
// absolute BodyPath. Populated on first LoadSkillBody call so subsequent
// activations (and the /context display) don't re-read the same file.
var fileBodyCache sync.Map // map[string]string

// LoadSkillBody returns the markdown body of a skill on demand. For file-based
// skills this reads Skill.BodyPath from disk; for bundled skills it looks up
// the embed-fs cache populated at LoadSkills time. The result is suitable for
// injection into the system prompt.
//
// Returns an error if the skill name is not present in skills, or if the body
// cannot be read.
func LoadSkillBody(skills []Skill, name string) (string, error) {
	skill, ok := FindSkill(skills, name)
	if !ok {
		return "", fmt.Errorf("skill %q not found", name)
	}
	if skill.Instruction != "" {
		// Already loaded (e.g. set by a caller after LoadSkills).
		return skill.Instruction, nil
	}
	if skill.Source == "bundled" {
		if v, ok := bundledBodyCache.Load(name); ok {
			if s, ok := v.(string); ok {
				return s, nil
			}
		}
		return "", fmt.Errorf("bundled skill %q has no cached body", name)
	}
	if skill.BodyPath == "" {
		return "", fmt.Errorf("skill %q has no BodyPath", name)
	}
	// Check the per-path cache before re-reading.
	if v, ok := fileBodyCache.Load(skill.BodyPath); ok {
		if s, ok := v.(string); ok {
			return s, nil
		}
	}
	data, err := os.ReadFile(skill.BodyPath)
	if err != nil {
		return "", fmt.Errorf("reading skill body %s: %w", skill.BodyPath, err)
	}
	// Re-parse to extract just the body (frontmatter and body are
	// delimited; we re-use the same parser to stay consistent).
	_, body, err := parseSkillContent(string(data), skill.Name)
	if err != nil {
		return "", fmt.Errorf("parsing skill body %s: %w", skill.BodyPath, err)
	}
	fileBodyCache.Store(skill.BodyPath, body)
	return body, nil
}

// SkillBodySize returns the size in bytes of a skill's body if it is already
// loaded (e.g. by a prior LoadSkillBody call or by a slash-activation that
// rebuilt the agent with this skill active). The second return value is
// false if the body has not been loaded — callers should use this to avoid
// triggering I/O just to render a /context display.
//
// For bundled skills the body is always considered loaded (it was read at
// LoadSkills time).
func SkillBodySize(skills []Skill, name string) (int, bool) {
	skill, ok := FindSkill(skills, name)
	if !ok {
		return 0, false
	}
	if skill.Instruction != "" {
		return len(skill.Instruction), true
	}
	if skill.Source == "bundled" {
		if v, ok := bundledBodyCache.Load(name); ok {
			if s, ok := v.(string); ok {
				return len(s), true
			}
		}
		return 0, false
	}
	if skill.BodyPath == "" {
		return 0, false
	}
	if v, ok := fileBodyCache.Load(skill.BodyPath); ok {
		if s, ok := v.(string); ok {
			return len(s), true
		}
	}
	return 0, false
}

// DefaultSkillDirs returns the default skill directories to search.
// It returns user-level (~/.pi-go/skills) plus project-level directories
// (.pi-go/skills, .claude/skills, .cursor/skills) found by walking up
// from the current working directory.
func DefaultSkillDirs() []string {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return DefaultSkillDirsIn(cwd)
}

// DefaultSkillDirsIn returns skill directories relative to the given root.
// User-level skill directory (~/.pi-go/skills) plus project-level directories
// (.pi-go/skills, .claude/skills, .cursor/skills) found by walking up from root.
func DefaultSkillDirsIn(root string) []string {
	dirs := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)

	// User-level skill directory.
	if homeDir, err := os.UserHomeDir(); err == nil {
		userDir := filepath.Join(homeDir, ".pi-go", "skills")
		if _, ok := seen[userDir]; !ok {
			seen[userDir] = struct{}{}
			dirs = append(dirs, userDir)
		}
	}

	// Project-level skill directories, walking up from root.
	for _, rel := range []string{
		filepath.Join(".pi-go", "skills"),
		filepath.Join(".claude", "skills"),
		filepath.Join(".cursor", "skills"),
	} {
		dir := findNearestDir(root, rel)
		if dir != "" {
			if _, ok := seen[dir]; !ok {
				seen[dir] = struct{}{}
				dirs = append(dirs, dir)
			}
		}
	}

	return dirs
}

// findNearestDir searches for rel starting at start and walking up the directory tree.
// Returns the first directory found, or empty string if not found.
func findNearestDir(start, rel string) string {
	dir := start
	for {
		candidate := filepath.Join(dir, rel)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
