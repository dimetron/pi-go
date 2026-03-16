package extension

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Skill represents a loaded skill from a SKILL.md file.
type Skill struct {
	// Name is the skill's identifier (derived from filename: my-skill.SKILL.md → my-skill).
	Name string
	// Description is a one-line description from frontmatter.
	Description string
	// Instruction is the markdown body (the system prompt to inject).
	Instruction string
	// Tools lists tool names this skill is allowed to use (from frontmatter).
	Tools []string
}

// LoadSkills discovers and loads SKILL.md files from the given directories.
// It searches for files matching *.SKILL.md pattern.
// Later directories override earlier ones (project overrides global).
func LoadSkills(dirs ...string) ([]Skill, error) {
	seen := make(map[string]int) // name → index in result
	var skills []Skill

	for _, dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*.SKILL.md"))
		if err != nil {
			return nil, fmt.Errorf("globbing skills in %s: %w", dir, err)
		}
		for _, path := range matches {
			skill, err := parseSkillFile(path)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", path, err)
			}
			if idx, ok := seen[skill.Name]; ok {
				// Override with project-level skill.
				skills[idx] = skill
			} else {
				seen[skill.Name] = len(skills)
				skills = append(skills, skill)
			}
		}
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

	// Derive name from filename: my-skill.SKILL.md → my-skill
	base := filepath.Base(path)
	name := strings.TrimSuffix(base, ".SKILL.md")

	skill := Skill{Name: name}

	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	inFrontmatter := false
	var body strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			// End of frontmatter.
			inFrontmatter = false
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
