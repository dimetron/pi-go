package scenarios

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/atif"
	"github.com/dimetron/pi-go/internal/eval"
)

// TestScenarios_CoverInventory is the coverage gate: every tool the agent can
// register must be targeted by at least one scenario or be explicitly
// excluded with a reason, and every scenario target and exclusion must name a
// tool that actually exists. A new tool with no eval fails here.
func TestScenarios_CoverInventory(t *testing.T) {
	inv, err := eval.Inventory(t.TempDir())
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(inv) < 20 {
		t.Fatalf("inventory suspiciously small: %d tools", len(inv))
	}

	if unmapped := eval.UnmappedTools(inv, Suite(), Exclusions); len(unmapped) > 0 {
		t.Errorf("registered tools with neither a scenario nor an exclusion: %s\n"+
			"add a scenario in internal/eval/scenarios/scenarios.go or an Exclusion with a reason",
			strings.Join(unmapped, ", "))
	}
	if unknown := eval.UnknownTargets(inv, Suite()); len(unknown) > 0 {
		t.Errorf("scenario targets that match no registered tool: %s", strings.Join(unknown, "; "))
	}

	names := make(map[string]bool, len(inv))
	for _, ti := range inv {
		names[ti.Name] = true
	}
	for _, ex := range Exclusions {
		if ex.Reason == "" {
			t.Errorf("exclusion %q has no reason", ex.Tool)
		}
		matched := false
		for name := range names {
			if strings.HasSuffix(ex.Tool, "*") && strings.HasPrefix(name, strings.TrimSuffix(ex.Tool, "*")) || ex.Tool == name {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("exclusion %q matches no registered tool (stale?)", ex.Tool)
		}
	}
}

func TestScenarios_WellFormed(t *testing.T) {
	seen := make(map[string]bool)
	for _, s := range Suite() {
		if s.Name == "" || strings.ContainsAny(s.Name, " /") {
			t.Errorf("scenario %q: name must be non-empty and path/space free", s.Name)
		}
		if seen[s.Name] {
			t.Errorf("duplicate scenario name %q", s.Name)
		}
		seen[s.Name] = true
		if strings.TrimSpace(s.Prompt) == "" {
			t.Errorf("scenario %q: empty prompt", s.Name)
		}
		if len(s.Tools) == 0 {
			t.Errorf("scenario %q: no target tools", s.Name)
		}
		for _, c := range s.Checks {
			if c.Tool != "" && !targetsTool(s, c.Tool) {
				t.Errorf("scenario %q: check %q refers to tool %q which is not a target", s.Name, c, c.Tool)
			}
			if c.Kind == "" {
				t.Errorf("scenario %q: check with empty kind", s.Name)
			}
		}
		if len(s.Modified) > 0 && !s.Git {
			t.Errorf("scenario %q: Modified set but Git is false", s.Name)
		}
		if _, ok := Lookup(s.Name); !ok {
			t.Errorf("Lookup(%q) failed", s.Name)
		}
	}
	if _, ok := Lookup("no-such-scenario"); ok {
		t.Error("Lookup of unknown name succeeded")
	}
}

func targetsTool(s eval.Scenario, tool string) bool {
	for _, target := range s.Tools {
		if target == tool {
			return true
		}
	}
	return false
}

// TestScenarios_Seed seeds every scenario's workspace and checks the fixtures
// and git state are what the prompts assume.
func TestScenarios_Seed(t *testing.T) {
	home := t.TempDir()
	for _, s := range Suite() {
		t.Run(s.Name, func(t *testing.T) {
			dir := t.TempDir()
			if err := eval.SeedWorkspace(s, dir, home); err != nil {
				t.Fatalf("SeedWorkspace: %v", err)
			}
			for name, want := range s.Files {
				if _, modified := s.Modified[name]; modified {
					continue
				}
				got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
				if err != nil || string(got) != want {
					t.Errorf("fixture %s: err=%v match=%v", name, err, string(got) == want)
				}
			}
			if !s.Git {
				if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
					t.Error("non-git scenario has a .git directory")
				}
				return
			}
			out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
			if err != nil {
				t.Fatalf("git status: %v", err)
			}
			for name := range s.Modified {
				if !strings.Contains(string(out), " M "+name) {
					t.Errorf("expected %s to be modified, git status:\n%s", name, out)
				}
			}
		})
	}
}

// TestScenarios_GitSatisfiable builds a synthetic trajectory that does what
// the git scenario asks for and checks the grading accepts it — i.e. the
// scenario's checks are satisfiable by a correct run, not over-constrained.
func TestScenarios_GitSatisfiable(t *testing.T) {
	s, _ := Lookup("git")
	dir := t.TempDir()
	if err := eval.SeedWorkspace(s, dir, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	traj := &atif.Trajectory{SessionID: "s1", Steps: []atif.Step{
		step(1, "git-overview", map[string]any{}, map[string]any{"branch": "main", "unstaged_files": []any{"main.go"}}),
		step(2, "git-file-diff", map[string]any{"file": "main.go"}, map[string]any{"file": "main.go", "diff": "-version 1\n+version 2\n"}),
		step(3, "git-hunk", map[string]any{"file": "main.go"}, map[string]any{"file": "main.go", "total_hunks": 1}),
	}}
	res := eval.EvaluateScenario(s, dir, roundTrip(t, traj))
	if res.Status != eval.StatusPass {
		t.Fatalf("git scenario not satisfiable by a correct run: %s", res.Reason)
	}
}

// TestScenarios_EditSatisfiable does the same for the edit scenario, which
// asserts on the filesystem rather than on results.
func TestScenarios_EditSatisfiable(t *testing.T) {
	s, _ := Lookup("edit")
	dir := t.TempDir()
	if err := eval.SeedWorkspace(s, dir, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "greet.go")
	data, _ := os.ReadFile(path)
	edited := strings.Replace(string(data), `"Hello"`, `"Howdy"`, 1)
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	traj := &atif.Trajectory{SessionID: "s1", Steps: []atif.Step{
		step(1, "read", map[string]any{"path": "greet.go"}, map[string]any{"content": data}),
		step(2, "edit", map[string]any{"path": "greet.go"}, map[string]any{"success": true}),
	}}
	res := eval.EvaluateScenario(s, dir, roundTrip(t, traj))
	if res.Status != eval.StatusPass {
		t.Fatalf("edit scenario not satisfiable: %s", res.Reason)
	}

	// An agent that only read and never edited fails on the tool requirement.
	untouched := t.TempDir()
	_ = eval.SeedWorkspace(s, untouched, t.TempDir())
	res = eval.EvaluateScenario(s, untouched, roundTrip(t, &atif.Trajectory{SessionID: "s2", Steps: []atif.Step{
		step(1, "read", map[string]any{"path": "greet.go"}, map[string]any{"content": data}),
	}}))
	if res.Status != eval.StatusFail || !strings.Contains(res.Reason, "tool edit: never called") {
		t.Fatalf("expected edit-never-called failure, got %s: %s", res.Status, res.Reason)
	}
}

// step builds one ATIF step with a single call and its paired observation.
func step(id int, fn string, args map[string]any, result map[string]any) atif.Step {
	callID := "c" + string(rune('0'+id))
	return atif.Step{
		StepID:    id,
		ToolCalls: []atif.ToolCall{{ToolCallID: callID, FunctionName: fn, Arguments: args}},
		Observation: &atif.Observation{Results: []atif.ObservationResult{{
			SourceCallID: callID,
			Content:      result,
		}}},
	}
}

// roundTrip writes the trajectory to disk and loads it back, so the fixture
// goes through the same JSON decoding as a real run (numbers become float64,
// maps become map[string]any).
func roundTrip(t *testing.T, traj *atif.Trajectory) []*eval.LoadedTrajectory {
	t.Helper()
	dir := t.TempDir()
	sd := filepath.Join(dir, traj.SessionID)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(traj)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sd, "trajectory.atif.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := eval.LoadTrajectories(dir)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}
