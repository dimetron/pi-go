package eval

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/atif"
)

func TestInventory_CoreAndOptionalGroups(t *testing.T) {
	inv, err := Inventory(t.TempDir())
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	byName := make(map[string]ToolInfo, len(inv))
	for _, ti := range inv {
		if ti.Name == "" {
			t.Errorf("tool with empty name in group %s", ti.Group)
		}
		if _, dup := byName[ti.Name]; dup {
			t.Errorf("duplicate tool name %q", ti.Name)
		}
		byName[ti.Name] = ti
	}
	// The core set the CLI always registers in print mode.
	for _, name := range []string{"read", "read_image", "write", "edit", "bash", "find", "ls", "tree",
		"git-overview", "git-file-diff", "git-hunk", "session-stats", "bash_wait", "bash_kill", "subagent"} {
		ti, ok := byName[name]
		if !ok {
			t.Errorf("core tool %q missing from inventory", name)
			continue
		}
		if ti.Requires != "" {
			t.Errorf("core tool %q should not have a requirement, got %q", name, ti.Requires)
		}
	}
	if _, grep := byName["grep"]; !grep {
		if _, rg := byName["ripgrep"]; !rg {
			t.Error("neither grep nor ripgrep in inventory")
		}
	}
	// Optional groups carry the capability they are gated on.
	for name, want := range map[string]string{
		"mem-search": "memory", "lsp-symbols": "lsp", "fetch_docs": "llms-config",
		"a2a": "a2a-config", "palace-status": "palace", "google_search": "gemini",
	} {
		ti, ok := byName[name]
		if !ok {
			t.Errorf("optional tool %q missing from inventory", name)
			continue
		}
		if ti.Requires != want {
			t.Errorf("%q requires %q, want %q", name, ti.Requires, want)
		}
	}
	if got := ToolNames(inv); len(got) != len(inv) || got[0] != inv[0].Name {
		t.Errorf("ToolNames mismatch: %v", got)
	}
}

// loadFixture writes the trajectories to a temp sessions dir and loads them
// back through LoadTrajectories, so content goes through JSON like a real run.
func loadFixture(t *testing.T, trajs ...*atif.Trajectory) []*LoadedTrajectory {
	t.Helper()
	dir := t.TempDir()
	for _, traj := range trajs {
		writeTraj(t, dir, traj.SessionID, traj)
	}
	loaded, err := LoadTrajectories(dir)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func callStep(id int, callID, fn string, args map[string]any, result any) atif.Step {
	s := atif.Step{
		StepID:    id,
		ToolCalls: []atif.ToolCall{{ToolCallID: callID, FunctionName: fn, Arguments: args}},
	}
	if result != nil {
		s.Observation = &atif.Observation{Results: []atif.ObservationResult{{SourceCallID: callID, Content: result}}}
	}
	return s
}

func TestComputeCoverage_Statuses(t *testing.T) {
	inv := []ToolInfo{
		{Name: "read", Group: "core"},
		{Name: "bash", Group: "core"},
		{Name: "write", Group: "core"},
		{Name: "a2a", Group: "a2a", Requires: "a2a-config"},
		{Name: "palace-status", Group: "palace", Requires: "palace"},
		{Name: "mystery", Group: "core"},
	}
	scenarios := []Scenario{
		{Name: "s-read", Tools: []string{"read"}},
		{Name: "s-bash", Tools: []string{"bash", "write"}},
		{Name: "s-read-2", Tools: []string{"read"}},
	}
	exclusions := []Exclusion{
		{Tool: "a2a", Reason: "external service"},
		{Tool: "palace-*", Reason: "needs palace"},
	}
	loaded := loadFixture(t, &atif.Trajectory{SessionID: "s1", Steps: []atif.Step{
		callStep(1, "c1", "read", map[string]any{"path": "a"}, map[string]any{"content": "x"}),
		callStep(2, "c2", "read", map[string]any{"path": "b"}, map[string]any{"error": "not found"}),
		callStep(3, "c3", "bash", map[string]any{"command": "false"}, map[string]any{"exit_code": 1, "stdout": ""}),
		callStep(4, "c4", "bash", map[string]any{"command": "x"}, nil), // wasted
		callStep(5, "c5", "mcp_thing", map[string]any{}, map[string]any{"ok": true}),
	}})

	cov := ComputeCoverage(inv, scenarios, exclusions, loaded)

	want := map[string]string{
		"read":          CoverageOK,
		"bash":          CoverageErrorsOnly,
		"write":         CoverageNotCalled,
		"a2a":           CoverageExcluded,
		"palace-status": CoverageExcluded,
		"mystery":       CoverageUnmapped,
	}
	rows := make(map[string]ToolCoverage)
	for _, row := range cov.Tools {
		rows[row.Name] = row
	}
	for name, status := range want {
		if rows[name].Status != status {
			t.Errorf("%s: status %q, want %q", name, rows[name].Status, status)
		}
	}
	if r := rows["read"]; r.Calls != 2 || r.Results != 2 || r.Errors != 1 || !reflect.DeepEqual(r.Scenarios, []string{"s-read", "s-read-2"}) {
		t.Errorf("read row = %+v", r)
	}
	if r := rows["bash"]; r.Calls != 2 || r.Results != 1 || r.Errors != 1 || r.Wasted != 1 {
		t.Errorf("bash row = %+v", r)
	}
	if rows["a2a"].Excluded != "external service" || rows["palace-status"].Excluded != "needs palace" {
		t.Errorf("exclusion reasons not carried: %+v / %+v", rows["a2a"], rows["palace-status"])
	}
	if cov.Total != 6 || cov.OK != 1 || cov.Errors != 1 || cov.NotCalled != 1 || cov.Excluded != 2 || cov.Unmapped != 1 {
		t.Errorf("summary = %+v", cov)
	}
	if !reflect.DeepEqual(cov.Gap, []string{"bash", "mystery", "write"}) {
		t.Errorf("gap = %v", cov.Gap)
	}
	if !reflect.DeepEqual(cov.Unknown, []string{"mcp_thing"}) {
		t.Errorf("unknown = %v", cov.Unknown)
	}
}

func TestComputeCoverage_ExcludedButCalledIsOK(t *testing.T) {
	inv := []ToolInfo{{Name: "a2a", Group: "a2a"}}
	loaded := loadFixture(t, &atif.Trajectory{SessionID: "s1", Steps: []atif.Step{
		callStep(1, "c1", "a2a", nil, map[string]any{"response": "hi"}),
	}})
	cov := ComputeCoverage(inv, nil, []Exclusion{{Tool: "a2a", Reason: "x"}}, loaded)
	if cov.Tools[0].Status != CoverageOK || cov.Tools[0].Excluded != "x" {
		t.Errorf("row = %+v", cov.Tools[0])
	}
}

func TestUnmappedAndUnknownTargets(t *testing.T) {
	inv := []ToolInfo{{Name: "read"}, {Name: "ripgrep"}, {Name: "write"}, {Name: "palace-a"}, {Name: "palace-b"}}
	scenarios := []Scenario{
		{Name: "a", Tools: []string{"read", "grep|ripgrep"}},
		{Name: "b", Tools: []string{"nope", "grep|rg"}},
	}
	exclusions := []Exclusion{{Tool: "palace-*", Reason: "r"}}

	if got := UnmappedTools(inv, scenarios, exclusions); !reflect.DeepEqual(got, []string{"write"}) {
		t.Errorf("UnmappedTools = %v", got)
	}
	if got := UnknownTargets(inv, scenarios); !reflect.DeepEqual(got, []string{"b: nope", "b: grep|rg"}) {
		t.Errorf("UnknownTargets = %v", got)
	}
}

func TestLooksLikeError_StructuredResults(t *testing.T) {
	cases := []struct {
		name    string
		content any
		want    bool
	}{
		{"error field", map[string]any{"error": "boom"}, true},
		{"empty error field", map[string]any{"error": "", "content": "ok"}, false},
		{"nonzero exit (float)", map[string]any{"exit_code": float64(2)}, true},
		{"nonzero exit (int)", map[string]any{"exit_code": 127}, true},
		{"zero exit", map[string]any{"exit_code": 0, "stdout": "hi"}, false},
		{"running (-1)", map[string]any{"exit_code": -1, "running": true}, false},
		{"plain map", map[string]any{"content": "error: nothing"}, false},
		{"string marker", "command not found", true},
		{"string clean", "all good", false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := looksLikeError(tc.content); got != tc.want {
			t.Errorf("%s: looksLikeError = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestComputeToolsMetrics_CountsStructuredErrors(t *testing.T) {
	loaded := loadFixture(t, &atif.Trajectory{SessionID: "s1", Steps: []atif.Step{
		callStep(1, "c1", "bash", map[string]any{"command": "false"}, map[string]any{"exit_code": 1}),
		callStep(2, "c2", "read", map[string]any{"path": "x"}, map[string]any{"error": "not found"}),
		callStep(3, "c3", "read", map[string]any{"path": "y"}, map[string]any{"content": "ok"}),
	}})
	m := ComputeToolsMetrics(loaded)
	if m.ByTool["bash"].Errors != 1 || m.ByTool["read"].Errors != 1 || m.ByTool["read"].Results != 2 {
		t.Errorf("by tool = %+v", m.ByTool)
	}
}

func TestExclusion_Matches(t *testing.T) {
	if !(Exclusion{Tool: "palace-*"}).matches("palace-search") {
		t.Error("glob should match prefix")
	}
	if (Exclusion{Tool: "palace-*"}).matches("mem-search") {
		t.Error("glob should not match other prefix")
	}
	if !(Exclusion{Tool: "a2a"}).matches("a2a") || (Exclusion{Tool: "a2a"}).matches("a2a-x") {
		t.Error("exact match semantics broken")
	}
}

// The JSON shape of the coverage report is consumed by the PR body / CI
// tooling; pin the field names.
func TestCoverage_JSONShape(t *testing.T) {
	cov := ComputeCoverage([]ToolInfo{{Name: "read", Group: "core"}}, []Scenario{{Name: "s", Tools: []string{"read"}}}, nil, nil)
	data, err := json.Marshal(cov)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"tools"`, `"total"`, `"ok"`, `"not_called"`, `"errors_only"`, `"excluded"`, `"unmapped"`, `"gap"`, `"status":"not-called"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("coverage JSON missing %s: %s", key, data)
		}
	}
}
