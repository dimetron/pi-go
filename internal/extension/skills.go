package extension

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/dimetron/pi-go/internal/audit"
)

// Skill represents a loaded skill from a SKILL.md file.
type Skill struct {
	// Name is the skill's identifier (derived from directory name).
	Name string
	// Description is a one-line description from frontmatter.
	Description string
	// Instruction is the markdown body (the system prompt to inject).
	Instruction string
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
			skill, err := parseSkillContent(string(mainFile), skillName)
			if err != nil {
				continue
			}
			skill.Source = "bundled"
			skillName := skill.Name
			seen[skillName] = len(skills)
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
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			skillFile := filepath.Join(dir, entry.Name(), "SKILL.md")
			if _, err := os.Stat(skillFile); err != nil {
				continue
			}

			// Audit the skill file before loading.
			if opts.AuditMode != AuditSkip {
				scanResult, scanErr := audit.ScanFile(skillFile)
				if scanErr != nil {
					fmt.Fprintf(os.Stderr, "pi-go: warning: audit scan failed for %s: %v\n", skillFile, scanErr)
				} else if scanResult.HasCritical() {
					if opts.AuditMode == AuditBlock {
						blocked = append(blocked, skillFile)
						fmt.Fprintf(os.Stderr, "pi-go: BLOCKED skill %s — critical hidden characters detected\n", entry.Name())
						continue
					}
					// AuditWarn: log but continue loading.
					fmt.Fprintf(os.Stderr, "pi-go: WARNING: skill %s has critical hidden characters\n", entry.Name())
				}
			}

			skill, err := parseSkillFile(skillFile)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", skillFile, err)
			}
			// Default name from directory if not set in frontmatter
			if skill.Name == "" {
				skill.Name = entry.Name()
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

// parseSkillFile reads a SKILL.md file with YAML-like frontmatter.
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

	return parseSkillContent(string(data), name)
}

// parseSkillContent parses skill content from a string with a given skill name.
// Used by both file-based and embedded skill loading.
func parseSkillContent(content, skillName string) (Skill, error) {
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

	skill.Instruction = strings.TrimSpace(body.String())
	return skill, scanner.Err()
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
