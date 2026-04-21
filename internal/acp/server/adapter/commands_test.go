package adapter

import (
	"testing"

	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/subagent"
)

func TestBuildAvailableCommands_MetaOnlyWhenEmpty(t *testing.T) {
	cmds := BuildAvailableCommands(nil, nil)

	want := []struct{ name, description string }{
		{"clear", "Reset the conversation"},
		{"compact", "Summarize and shorten history"},
		{"help", "List available commands"},
	}
	if got := len(cmds); got != len(want) {
		t.Fatalf("len(cmds) = %d, want %d (%+v)", got, len(want), cmds)
	}
	for i, w := range want {
		if cmds[i].Name != w.name {
			t.Errorf("cmds[%d].Name = %q, want %q", i, cmds[i].Name, w.name)
		}
		if cmds[i].Description != w.description {
			t.Errorf("cmds[%d].Description = %q, want %q", i, cmds[i].Description, w.description)
		}
	}
}

func TestBuildAvailableCommands_AppendsSkillsInOrder(t *testing.T) {
	skills := []extension.Skill{
		{Name: "code-review", Description: "Review a pull request"},
		{Name: "explore", Description: "Search the codebase"},
	}
	cmds := BuildAvailableCommands(skills, nil)

	if got, want := len(cmds), len(metaCommands)+len(skills); got != want {
		t.Fatalf("len(cmds) = %d, want %d", got, want)
	}
	base := len(metaCommands)
	for i, sk := range skills {
		if cmds[base+i].Name != sk.Name {
			t.Errorf("cmds[%d].Name = %q, want %q", base+i, cmds[base+i].Name, sk.Name)
		}
		if cmds[base+i].Description != sk.Description {
			t.Errorf("cmds[%d].Description = %q, want %q", base+i, cmds[base+i].Description, sk.Description)
		}
	}
}

func TestBuildAvailableCommands_AppendsSubagentsInOrder(t *testing.T) {
	agents := []subagent.AgentConfig{
		{Name: "explore", Description: "Fast codebase exploration"},
		{Name: "plan", Description: "Draft an implementation plan"},
	}
	cmds := BuildAvailableCommands(nil, agents)

	if got, want := len(cmds), len(metaCommands)+len(agents); got != want {
		t.Fatalf("len(cmds) = %d, want %d", got, want)
	}
	base := len(metaCommands)
	for i, sa := range agents {
		if cmds[base+i].Name != sa.Name {
			t.Errorf("cmds[%d].Name = %q, want %q", base+i, cmds[base+i].Name, sa.Name)
		}
		if cmds[base+i].Description != sa.Description {
			t.Errorf("cmds[%d].Description = %q, want %q", base+i, cmds[base+i].Description, sa.Description)
		}
	}
}

func TestBuildAvailableCommands_CombinedOrderIsMetaThenSkillsThenSubagents(t *testing.T) {
	skills := []extension.Skill{{Name: "code-review", Description: "skill-desc"}}
	agents := []subagent.AgentConfig{{Name: "planner", Description: "agent-desc"}}

	cmds := BuildAvailableCommands(skills, agents)

	if got, want := len(cmds), len(metaCommands)+len(skills)+len(agents); got != want {
		t.Fatalf("len(cmds) = %d, want %d", got, want)
	}
	// Meta occupies slots 0..len(metaCommands)-1.
	for i, m := range metaCommands {
		if cmds[i].Name != m.Name {
			t.Errorf("meta[%d] = %q, want %q", i, cmds[i].Name, m.Name)
		}
	}
	if got, want := cmds[len(metaCommands)].Name, "code-review"; got != want {
		t.Errorf("skill slot = %q, want %q", got, want)
	}
	if got, want := cmds[len(metaCommands)+1].Name, "planner"; got != want {
		t.Errorf("subagent slot = %q, want %q", got, want)
	}
}

func TestBuildAvailableCommands_SkipsUnnamedEntries(t *testing.T) {
	// Malformed frontmatter or manual zero-value entries must not produce
	// unnamed slash commands — those would render as a blank item in Zed's
	// autocomplete, which is confusing.
	skills := []extension.Skill{
		{Name: "", Description: "nameless skill"},
		{Name: "real", Description: "loaded skill"},
	}
	agents := []subagent.AgentConfig{
		{Name: "", Description: "nameless agent"},
		{Name: "worker", Description: "loaded agent"},
	}

	cmds := BuildAvailableCommands(skills, agents)

	if got, want := len(cmds), len(metaCommands)+2; got != want {
		t.Fatalf("len(cmds) = %d, want %d (%+v)", got, want, cmds)
	}
	names := make([]string, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	for _, n := range names {
		if n == "" {
			t.Errorf("unexpected empty command name in %+v", names)
		}
	}
}

func TestBuildAvailableCommands_ReturnedSliceIsIndependent(t *testing.T) {
	// BuildAvailableCommands must not mutate or share backing storage with
	// the package-level metaCommands slice — callers (Initialize) may
	// append their own entries in future layers.
	first := BuildAvailableCommands(nil, nil)
	first[0].Name = "mutated"

	second := BuildAvailableCommands(nil, nil)
	if second[0].Name == "mutated" {
		t.Fatalf("metaCommands leaked into caller: %+v", second)
	}
}
