package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/extension"
)

// The repo's own AGENTS.md is injected into every pi-go session started here,
// so its size is paid on every request. It was 38 KiB before the detail moved
// into .pi-go/skills; these tests keep it from growing back, and keep the
// pointers it now relies on from dangling.

// maxProjectRulesBytes is the budget for the root AGENTS.md. Raise it only
// with a reason: a rule that needs paragraphs belongs in a skill.
const maxProjectRulesBytes = 8 * 1024

// maxSkillDescriptionBytes bounds one line of the skills menu, which is also
// sent on every request.
const maxSkillDescriptionBytes = 300

func readProjectRules(t *testing.T) (root, rules string) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may carry CRLF; the budget is about content.
	return root, strings.ReplaceAll(string(data), "\r\n", "\n")
}

// referencedSkills returns the skill names in the "Read" column of the
// AGENTS.md routing table: rows shaped `| <when> | `name` |`.
func referencedSkills(rules string) []string {
	row := regexp.MustCompile("(?m)^\\|.*\\|\\s*`([a-z0-9-]+)`\\s*\\|\\s*$")
	var names []string
	for _, m := range row.FindAllStringSubmatch(rules, -1) {
		names = append(names, m[1])
	}
	return names
}

func TestProjectRules_StayWithinBudget(t *testing.T) {
	_, rules := readProjectRules(t)
	if len(rules) > maxProjectRulesBytes {
		t.Errorf("AGENTS.md is %d bytes, over the %d-byte budget: move detail into a skill under .pi-go/skills",
			len(rules), maxProjectRulesBytes)
	}
}

// Slimming must not drop a rule that has to hold in every session. Each anchor
// is the load-bearing token of one such rule.
func TestProjectRules_KeepAlwaysOnRules(t *testing.T) {
	_, rules := readProjectRules(t)
	for _, anchor := range []string{
		"git worktree add",                // work in a worktree
		"git commit -s -S",                // signed and signed-off
		"git verify-commit",               // verify, do not grep gpgsig
		"--no-verify",                     // never bypass hooks
		"Rebase and\n  merge",             // never rebase-merge
		"session link",                    // no agent session links
		"make vulncheck",                  // post-review scan
		"os.Stderr",                       // TUI output safety
		"extension.ComposeAfterToolChain", // after-tool chain
		"tools.NewSandbox",                // Windows: close the sandbox
		"Tooling is Go, not Python",       // no interpreter dependency
		".pi-go/skills/<name>/SKILL.md",   // where the detail went
	} {
		if !strings.Contains(rules, anchor) {
			t.Errorf("AGENTS.md no longer contains %q", anchor)
		}
	}
}

func TestProjectRules_ReferencedSkillsLoad(t *testing.T) {
	root, rules := readProjectRules(t)

	names := referencedSkills(rules)
	if len(names) < 5 {
		t.Fatalf("found %d skills in the AGENTS.md routing table, want at least 5: %v", len(names), names)
	}

	skills, err := extension.LoadSkills(filepath.Join(root, ".pi-go", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	byName := make(map[string]extension.Skill, len(skills))
	for _, s := range skills {
		byName[s.Name] = s
	}

	menu := appendSkillsMenu(skills)
	for _, name := range names {
		s, ok := byName[name]
		if !ok {
			t.Errorf("AGENTS.md routes to skill %q, which does not load from .pi-go/skills", name)
			continue
		}
		// A folded scalar ("description: >") parses as ">" and leaves the model
		// with nothing to match the skill on.
		if len(s.Description) < 40 {
			t.Errorf("skill %q has description %q: too short to route on", name, s.Description)
		}
		if len(s.Description) > maxSkillDescriptionBytes {
			t.Errorf("skill %q description is %d bytes, over %d: it is sent on every request",
				name, len(s.Description), maxSkillDescriptionBytes)
		}
		if !strings.Contains(menu, "- /"+name+": ") {
			t.Errorf("skill %q is missing from the skills menu", name)
		}
		body, err := extension.LoadSkillBody(skills, name)
		if err != nil {
			t.Errorf("skill %q body: %v", name, err)
			continue
		}
		if len(strings.TrimSpace(body)) < 500 {
			t.Errorf("skill %q body is %d bytes: the detail AGENTS.md points at is not there", name, len(body))
		}
	}
}

// The detail that left AGENTS.md has to exist somewhere. One distinctive
// phrase per moved section, checked against the skill that now owns it.
func TestProjectRules_MovedDetailLandedInSkills(t *testing.T) {
	root, _ := readProjectRules(t)
	for skill, phrases := range map[string][]string{
		"git-workflow": {
			"gpg.ssh.allowedSignersFile",
			"Where worktrees live",
			"PI_ALLOW_LARGE_FILES",
			"git hash-object -t commit -w",
		},
		"pr-workflow": {
			"codex exec --sandbox danger-full-access",
			"allowed_merge_methods",
			"claude.ai/code/session_",
			"go run ./hack/vulngate",
		},
		"windows-ci":            {"TempDir RemoveAll cleanup", "rgAvailable", "--allow-escape-sequences"},
		"tool-compaction":       {"invokeAfterToolCallbacks", "primaryOutputField", "never_worse", "CompactWrite"},
		"proof-check":           {"Bound the input by what the producer can emit", "Verify the guard can fail"},
		"pi-check-session-logs": {"trajectory.atif.json", "last-session.json"},
	} {
		data, err := os.ReadFile(filepath.Join(root, ".pi-go", "skills", skill, "SKILL.md"))
		if err != nil {
			t.Errorf("skill %q: %v", skill, err)
			continue
		}
		for _, p := range phrases {
			if !strings.Contains(string(data), p) {
				t.Errorf("skill %q no longer contains %q", skill, p)
			}
		}
	}
}
