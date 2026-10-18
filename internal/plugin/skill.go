package plugin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillsDir returns the user-level skills directory under a pi-go home. Skills
// installed here are loaded by every session and outrank plugin skills, so an
// explicit install always beats a bundled copy of the same name.
func SkillsDir(piHome string) string {
	return filepath.Join(piHome, "skills")
}

// SkillSource is one skill found in a repository or a local directory.
type SkillSource struct {
	// Name is the skill's directory name, which is how pi-go refers to it.
	Name string
	// Dir is the absolute path of the directory holding SKILL.md.
	Dir string
	// Description is the frontmatter description, empty when the file records
	// none or cannot be read.
	Description string
}

// InstalledSkill records what an install put on disk.
type InstalledSkill struct {
	// Name is the skill name, also its directory under the skills directory.
	Name string
	// Path is the installed directory.
	Path string
	// Description is the frontmatter description, when it could be read.
	Description string
}

// DiscoverSkills finds every skill in root.
//
// Two layouts are recognized, because both are in use in the wild: a directory
// named "skills" whose children are skill directories (which covers
// skills/<name>/SKILL.md, .pi-go/skills/<name>/SKILL.md, and the
// plugins/<plugin>/skills/<name>/SKILL.md shape marketplaces use), and root
// itself when it holds a SKILL.md. Only the immediate children of a skills
// directory are examined: a skill's own references/ and scripts/ subdirectories
// are part of the skill, not skills of their own.
func DiscoverSkills(root string) ([]SkillSource, error) {
	if ok, err := isSkillDir(root); err != nil {
		return nil, err
	} else if ok {
		return []SkillSource{newSkillSource(root)}, nil
	}

	var found []SkillSource
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if path == root || d.Name() != "skills" {
			return nil
		}
		children, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			dir := filepath.Join(path, child.Name())
			ok, err := isSkillDir(dir)
			if err != nil {
				return err
			}
			if ok {
				found = append(found, newSkillSource(dir))
			}
		}
		return filepath.SkipDir
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found, nil
}

// isSkillDir reports whether dir is a skill directory: one holding a readable
// SKILL.md.
func isSkillDir(dir string) (bool, error) {
	info, err := os.Stat(filepath.Join(dir, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !info.IsDir(), nil
}

// newSkillSource builds a SkillSource for a directory already known to hold a
// SKILL.md.
func newSkillSource(dir string) SkillSource {
	return SkillSource{
		Name:        filepath.Base(dir),
		Dir:         dir,
		Description: skillDescription(dir),
	}
}

// skillDescription reads the frontmatter description from a skill directory.
// Any read or parse problem yields an empty description: a skill with no
// description is still installable, so this must never fail an install.
func skillDescription(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return ""
	}
	block, ok := splitFrontmatter(string(raw))
	if !ok {
		return ""
	}
	for _, line := range strings.Split(block, "\n") {
		key, value, found := strings.Cut(line, ":")
		if found && strings.TrimSpace(key) == "description" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// splitFrontmatter returns the text between the opening and closing "---" lines.
//
// Frontmatter written on Windows carries CRLF endings, and an editor may leave
// a byte-order mark; both would otherwise stop the opening delimiter from
// matching, turning every field into body text.
func splitFrontmatter(content string) (string, bool) {
	normalized := strings.ReplaceAll(strings.TrimPrefix(content, "\ufeff"), "\r\n", "\n")
	body, found := strings.CutPrefix(normalized, "---\n")
	if !found {
		return "", false
	}
	block, _, found := strings.Cut(body, "\n---")
	if !found {
		return "", false
	}
	return block, true
}

// InstallSkills installs skills from source into the user-level skills
// directory and returns what it installed.
//
// source is anything NormalizeSource accepts: an owner/repo shorthand, a git
// URL, or a local directory. names selects skills by name; with none, every
// skill the source contains is installed. An existing skill directory is
// refused unless force is set, so an install cannot silently overwrite a skill
// the user wrote or edited by hand.
func (m *Manager) InstallSkills(ctx context.Context, source string, names []string, force bool) ([]InstalledSkill, error) {
	normalized := NormalizeSource(source)
	if normalized == "" {
		return nil, errors.New("no skill source given")
	}

	repoDir := normalized
	if !isLocalDir(normalized) {
		if err := ensureGit(ctx); err != nil {
			return nil, err
		}
		cloneCtx, cancel := context.WithTimeout(ctx, cloneTimeout)
		defer cancel()

		tmp := filepath.Join(Root(m.PiHome), "tmp-skill")
		if err := os.RemoveAll(tmp); err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)

		m.logf("cloning %s", normalized)
		if err := cloneAt(cloneCtx, normalized, "", tmp); err != nil {
			return nil, err
		}
		repoDir = tmp
	}

	available, err := DiscoverSkills(repoDir)
	if err != nil {
		return nil, err
	}
	if len(available) == 0 {
		return nil, fmt.Errorf("no skills found in %s (looked for skills/<name>/SKILL.md and .pi-go/skills/<name>/SKILL.md)", source)
	}

	selected, err := selectSkills(available, names)
	if err != nil {
		return nil, err
	}

	destRoot := SkillsDir(m.PiHome)
	installed := make([]InstalledSkill, 0, len(selected))
	for _, skill := range selected {
		if err := ValidName(skill.Name); err != nil {
			return nil, fmt.Errorf("skill name from %s is unusable: %w", source, err)
		}
		dst := filepath.Join(destRoot, skill.Name)
		// Checked before anything is removed: a source that resolves inside
		// its own destination would otherwise have its files deleted and then
		// be refused, losing what the user asked to install.
		if insideDir(dst, skill.Dir) {
			return nil, fmt.Errorf("refusing to install skill %q into %s, which contains its own source", skill.Name, dst)
		}
		if _, err := os.Stat(dst); err == nil {
			if !force {
				return nil, fmt.Errorf("skill %q is already installed at %s — replace it with --force", skill.Name, dst)
			}
			if err := os.RemoveAll(dst); err != nil {
				return nil, err
			}
		}
		if err := os.MkdirAll(destRoot, 0o755); err != nil {
			return nil, err
		}
		if err := copyTree(skill.Dir, dst); err != nil {
			return nil, fmt.Errorf("installing skill %s: %w", skill.Name, err)
		}
		m.logf("installed skill %s", skill.Name)
		installed = append(installed, InstalledSkill{
			Name:        skill.Name,
			Path:        dst,
			Description: skill.Description,
		})
	}
	return installed, nil
}

// selectSkills narrows discovered skills to the requested names. An unknown
// name is an error that lists what the source does offer, because a typo in
// --skill is otherwise indistinguishable from a source that has no skills.
func selectSkills(all []SkillSource, names []string) ([]SkillSource, error) {
	if len(names) == 0 {
		return all, nil
	}
	byName := make(map[string][]SkillSource, len(all))
	available := make([]string, 0, len(all))
	for _, skill := range all {
		byName[skill.Name] = append(byName[skill.Name], skill)
		available = append(available, skill.Name)
	}

	selected := make([]SkillSource, 0, len(names))
	for _, name := range names {
		candidates := byName[name]
		switch len(candidates) {
		case 0:
			return nil, fmt.Errorf("skill %q not found; available: %s", name, strings.Join(available, ", "))
		case 1:
			selected = append(selected, candidates[0])
		default:
			dirs := make([]string, 0, len(candidates))
			for _, c := range candidates {
				dirs = append(dirs, c.Dir)
			}
			return nil, fmt.Errorf("skill %q appears %d times in the source (%s) — install it from a narrower source",
				name, len(candidates), strings.Join(dirs, ", "))
		}
	}
	return selected, nil
}

// ListInstalledSkills returns the skills directly under a pi-go home's skills
// directory, ordered by name. A directory without a SKILL.md is not a skill and
// is skipped.
func ListInstalledSkills(piHome string) ([]InstalledSkill, error) {
	dir := SkillsDir(piHome)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	skills := make([]InstalledSkill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		ok, err := isSkillDir(path)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		skills = append(skills, InstalledSkill{
			Name:        entry.Name(),
			Path:        path,
			Description: skillDescription(path),
		})
	}
	sort.Slice(skills, func(i, j int) bool { return skills[i].Name < skills[j].Name })
	return skills, nil
}

// RemoveSkills deletes the named skills from the user-level skills directory
// and reports which names were not installed. Installed skills are still
// returned as removed even when a later name is missing, so a partial command
// reports exactly what happened.
func RemoveSkills(piHome string, names []string) (removed, missing []string) {
	dir := SkillsDir(piHome)
	for _, name := range names {
		path := filepath.Join(dir, name)
		ok, err := isSkillDir(path)
		if err != nil || !ok {
			missing = append(missing, name)
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			missing = append(missing, name)
			continue
		}
		removed = append(removed, name)
	}
	return removed, missing
}
