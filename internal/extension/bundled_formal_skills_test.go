package extension

import "testing"

// Every verify-* skill must be embedded and its frontmatter must parse, or the
// skill silently never registers and the model it documents is unreachable from
// an agent session.
func TestBundledFormalVerificationSkillsRegistered(t *testing.T) {
	want := []string{"verify-goal-loop", "verify-run-pipeline"}

	skills, err := LoadBundledSkills()
	if err != nil {
		t.Fatalf("LoadBundledSkills: %v", err)
	}
	for _, name := range want {
		files, ok := skills[name]
		if !ok {
			t.Errorf("bundled skill %q is not embedded; its model has no discoverable entry point", name)
			continue
		}
		var body []byte
		for _, f := range files {
			if len(f.RelPath) >= len("SKILL.md") && f.RelPath[len(f.RelPath)-len("SKILL.md"):] == "SKILL.md" {
				body = f.Content
			}
			if body != nil && len(body) == 0 {
				t.Errorf("%s: SKILL.md is empty", name)
			}
		}
		if len(body) == 0 {
			t.Errorf("%s: no non-empty SKILL.md", name)
		}
	}
}
