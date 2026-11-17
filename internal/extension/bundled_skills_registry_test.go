package extension

import (
	"strings"
	"testing"
)

// Every bundled skill must be embedded and its frontmatter must parse, or the
// skill silently never registers and the workflow it documents is unreachable
// from an agent session.
func TestBundledSkillsRegistered(t *testing.T) {
	want := []string{
		"agents-md",
		"memory-index",
		"osx-tuning",
		"ponytail",
		"rex-hunk-diff",
		"verify-agent-lifecycle",
		"verify-goal-loop",
		"verify-history-rewrite",
		"verify-run-pipeline",
		"verify-session-flow",
	}

	skills, err := LoadBundledSkills()
	if err != nil {
		t.Fatalf("LoadBundledSkills: %v", err)
	}
	for _, name := range want {
		files, ok := skills[name]
		if !ok {
			t.Errorf("bundled skill %q is not embedded; its workflow has no discoverable entry point", name)
			continue
		}
		if len(skillBody(files)) == 0 {
			t.Errorf("%s: no non-empty SKILL.md", name)
		}
	}
}

// A skill's description is what the agent reads when deciding whether to invoke
// it, so it has to be present and long enough to be actionable. An empty or
// one-word description registers a skill nothing will ever load.
func TestBundledSkillsCarryADescription(t *testing.T) {
	skills, err := LoadBundledSkills()
	if err != nil {
		t.Fatalf("LoadBundledSkills: %v", err)
	}
	for name, files := range skills {
		body := skillBody(files)
		if len(body) == 0 {
			t.Errorf("%s: no SKILL.md", name)
			continue
		}
		parsed, _, err := parseSkillContent(string(body), name)
		if err != nil {
			t.Errorf("%s: frontmatter does not parse: %v", name, err)
			continue
		}
		if strings.TrimSpace(parsed.Description) == "" {
			t.Errorf("%s: empty description — nothing tells an agent when to use it", name)
			continue
		}
		if len(parsed.Description) < 80 {
			t.Errorf("%s: description is %d chars; it needs enough detail to be actionable",
				name, len(parsed.Description))
		}
	}
}

// skillBody returns the SKILL.md bytes from a bundled skill's file set.
func skillBody(files []BundledSkillFile) []byte {
	for _, f := range files {
		if strings.HasSuffix(f.RelPath, "SKILL.md") {
			return f.Content
		}
	}
	return nil
}
