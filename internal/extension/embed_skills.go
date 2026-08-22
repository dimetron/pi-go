package extension

import (
	"embed"
	"io/fs"
	"strings"
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
		collectBundledSkillFile(skillFiles, path, d, err)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return groupBundledSkillFiles(skillFiles), nil
}

// collectBundledSkillFile records one walked entry under its skill name.
// Everything that is not a readable file inside a skill subdirectory is
// skipped rather than reported: an inaccessible entry, a directory, a path
// with no skill subdirectory, and a file that fails to read are all
// non-fatal — a single bad entry must not cost the binary every other skill.
func collectBundledSkillFile(skillFiles map[string]map[string][]byte, path string, d fs.DirEntry, err error) {
	if err != nil || d.IsDir() {
		return
	}
	skillName, ok := bundledSkillName(path)
	if !ok {
		return
	}
	content, readErr := fs.ReadFile(bundledSkillsFS, path)
	if readErr != nil {
		return
	}
	if skillFiles[skillName] == nil {
		skillFiles[skillName] = make(map[string][]byte)
	}
	skillFiles[skillName][path] = content
}

// bundledSkillName extracts the skill directory name from an embedded path
// like "bundled_skills/agents-md/SKILL.md". A path with nothing after the
// prefix, or with no subdirectory under it, belongs to no skill.
func bundledSkillName(path string) (string, bool) {
	const prefix = "bundled_skills/"
	if len(path) <= len(prefix) {
		return "", false
	}
	rest := path[len(prefix):] // "agents-md/SKILL.md"
	slashIdx := strings.IndexByte(rest, '/')
	if slashIdx < 0 {
		return "", false // no subdir, skip
	}
	return rest[:slashIdx], true
}

// groupBundledSkillFiles flattens the per-skill path→content map into the
// public result type.
func groupBundledSkillFiles(skillFiles map[string]map[string][]byte) map[string][]BundledSkillFile {
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
	return result
}
