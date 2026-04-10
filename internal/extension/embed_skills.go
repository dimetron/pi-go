package extension

import (
	"embed"
	"io/fs"
)

// bundledSkillsFS embeds all skills in the bundled_skills directory.
//
//go:embed bundled_skills/*/SKILL.md bundled_skills/*/*.md
var bundledSkillsFS embed.FS

// BundledSkillFile represents a skill loaded from the embedded filesystem.
type BundledSkillFile struct {
	SkillName string
	RelPath   string
	Content   []byte
}

// LoadBundledSkills loads all embedded skill files.
// Returns a map of skill name → []BundledSkillFile (SKILL.md + supporting files).
func LoadBundledSkills() (map[string][]BundledSkillFile, error) {
	// Map of skill name → files accumulated during WalkDir.
	// We walk the whole tree and collect files per skill.
	skillFiles := make(map[string]map[string][]byte) // skillName → relPath → content

	err := fs.WalkDir(bundledSkillsFS, "bundled_skills", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // skip inaccessible entries — embedded fs may have restricted files
		}
		if d.IsDir() {
			return nil
		}
		// path is like "bundled_skills/agents-md/SKILL.md"
		// Extract skill name: "bundled_skills/" = 15 chars
		if len(path) <= 15 {
			return nil
		}
		rest := path[15:] // "agents-md/SKILL.md"
		slashIdx := -1
		for i := 0; i < len(rest); i++ {
			if rest[i] == '/' {
				slashIdx = i
				break
			}
		}
		if slashIdx < 0 {
			return nil // no subdir, skip
		}
		skillName := rest[:slashIdx]

		content, err := fs.ReadFile(bundledSkillsFS, path)
		if err != nil {
			return nil //nolint:nilerr // skip files that fail to read — not fatal
		}

		if skillFiles[skillName] == nil {
			skillFiles[skillName] = make(map[string][]byte)
		}
		skillFiles[skillName][path] = content
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Convert to the result type.
	result := make(map[string][]BundledSkillFile)
	for skillName, files := range skillFiles {
		var sorted []BundledSkillFile
		for relPath, content := range files {
			sorted = append(sorted, BundledSkillFile{
				SkillName: skillName,
				RelPath:   relPath,
				Content:   content,
			})
		}
		result[skillName] = sorted
	}
	return result, nil
}
