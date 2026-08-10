package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/atif"
)

// writeTraj writes traj to <dir>/<sessionID>/trajectory.atif.json and returns
// the absolute path to the file.
func writeTraj(t *testing.T, dir, sessionID string, traj *atif.Trajectory) string {
	t.Helper()
	sd := filepath.Join(dir, sessionID)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(sd, "trajectory.atif.json")
	data, err := json.Marshal(traj)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func baseTraj(sessionID, agentName string) *atif.Trajectory {
	return &atif.Trajectory{
		SessionID: sessionID,
		Agent: atif.AgentInfo{
			Name:      agentName,
			ModelName: "test-model",
		},
	}
}

func TestComputeTrajectoryMetrics_DepthAndRefs(t *testing.T) {
	dir := t.TempDir()

	// child first so path exists for the parent's ref.
	child := baseTraj("child", "task")
	childPath := writeTraj(t, dir, "child", child)

	parent := baseTraj("parent", "task")
	parent.Steps = []atif.Step{
		{
			StepID:    1,
			Timestamp: time.Now().Add(-2 * time.Second).Format(time.RFC3339Nano),
			ToolCalls: []atif.ToolCall{{ToolCallID: "call-1", FunctionName: "bash"}},
			Observation: &atif.Observation{
				Results: []atif.ObservationResult{{
					SourceCallID:          "call-1",
					Content:               "ok",
					SubagentTrajectoryRef: childPath,
				}},
			},
		},
		{
			StepID:    2,
			Timestamp: time.Now().Format(time.RFC3339Nano),
		},
	}
	writeTraj(t, dir, "parent", parent)

	loaded, err := LoadTrajectories(dir)
	if err != nil {
		t.Fatalf("LoadTrajectories: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded %d trajectories, want 2", len(loaded))
	}

	m := ComputeTrajectoryMetrics(loaded)
	if m.NestedAgentCalls != 1 {
		t.Errorf("NestedAgentCalls = %d, want 1", m.NestedAgentCalls)
	}
	if m.MaxDepth != 1 {
		t.Errorf("MaxDepth = %d, want 1", m.MaxDepth)
	}
	if m.TotalSteps != 2 {
		t.Errorf("TotalSteps = %d, want 2", m.TotalSteps)
	}
	if m.TotalToolCalls != 1 {
		t.Errorf("TotalToolCalls = %d, want 1", m.TotalToolCalls)
	}

	byID := map[string]SessionSummary{}
	for _, s := range m.Sessions {
		byID[s.SessionID] = s
	}
	if byID["parent"].Depth != 0 {
		t.Errorf("parent depth = %d, want 0", byID["parent"].Depth)
	}
	if byID["child"].Depth != 1 {
		t.Errorf("child depth = %d, want 1", byID["child"].Depth)
	}
	if byID["parent"].SubagentRefs != 1 {
		t.Errorf("parent subagent refs = %d, want 1", byID["parent"].SubagentRefs)
	}
	if byID["child"].SubagentRefs != 0 {
		t.Errorf("child subagent refs = %d, want 0", byID["child"].SubagentRefs)
	}
	// Sessions sorted depth-ascending: parent (0) before child (1).
	if m.Sessions[0].SessionID != "parent" || m.Sessions[1].SessionID != "child" {
		t.Errorf("session ordering = %v, want [parent child]", m.Sessions)
	}
}

func TestComputeTrajectoryMetrics_LoadSkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	writeTraj(t, dir, "good", baseTraj("good", "task"))

	// A corrupt file in a session dir should be skipped, not fail the load.
	badDir := filepath.Join(dir, "broken")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "trajectory.atif.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadTrajectories(dir)
	if err != nil {
		t.Fatalf("LoadTrajectories with malformed file: %v", err)
	}
	if len(loaded) != 1 || loaded[0].SessionID != "good" {
		t.Fatalf("loaded = %v, want only 'good'", loaded)
	}
}

func TestComputeConcurrencyMetrics(t *testing.T) {
	base := time.Now()
	samples := []ConcurrencySample{
		{Timestamp: base, Running: 0, AgentIDs: nil},
		{Timestamp: base.Add(50 * time.Millisecond), Running: 2, AgentIDs: []string{"a", "b"}},
		{Timestamp: base.Add(100 * time.Millisecond), Running: 2, AgentIDs: []string{"b", "c"}},
		{Timestamp: base.Add(150 * time.Millisecond), Running: 1, AgentIDs: []string{"c"}},
	}
	m := ComputeConcurrencyMetrics(4, samples)
	if m.PoolBudget != 4 {
		t.Errorf("PoolBudget = %d, want 4", m.PoolBudget)
	}
	if m.WorkerBudget != 2 {
		t.Errorf("WorkerBudget = %d, want 2", m.WorkerBudget)
	}
	if m.MaxRunning != 2 {
		t.Errorf("MaxRunning = %d, want 2", m.MaxRunning)
	}
	if m.MeanRunning != 1.25 {
		t.Errorf("MeanRunning = %v, want 1.25", m.MeanRunning)
	}
	if m.AgentsSeen != 3 { // a, b, c
		t.Errorf("AgentsSeen = %d, want 3", m.AgentsSeen)
	}
	if m.ParallelOverlap != 0.5 { // 2 of 4 samples had >1 running
		t.Errorf("ParallelOverlap = %v, want 0.5", m.ParallelOverlap)
	}
	if len(m.Samples) != 4 {
		t.Errorf("Samples kept = %d, want 4", len(m.Samples))
	}

	// Budget of 1 floors the worker budget at 1.
	if got := childBudget(1); got != 1 {
		t.Errorf("childBudget(1) = %d, want 1", got)
	}
}

func TestComputeToolsMetrics(t *testing.T) {
	dir := t.TempDir()
	callTS := time.Now().Add(-1 * time.Second).Format(time.RFC3339Nano)
	obsTS := time.Now().Add(-500 * time.Millisecond).Format(time.RFC3339Nano)

	traj := baseTraj("t1", "task")
	traj.Steps = []atif.Step{
		{
			StepID:    1,
			Timestamp: callTS,
			ToolCalls: []atif.ToolCall{
				{ToolCallID: "c1", FunctionName: "bash", Arguments: map[string]any{"command": "go test ./..."}},
				{ToolCallID: "c2", FunctionName: "bash", Arguments: map[string]any{"command": "go test ./..."}}, // duplicate of c1
				{ToolCallID: "c3", FunctionName: "edit", Arguments: map[string]any{"file": "a.go"}},
				{ToolCallID: "c4", FunctionName: "subagent", Arguments: map[string]any{"type": "explore"}},
			},
		},
		{
			StepID:    2,
			Timestamp: obsTS,
			Observation: &atif.Observation{
				Results: []atif.ObservationResult{
					{SourceCallID: "c1", Content: "ok"},
					{SourceCallID: "c3", Content: "error: edit failed"},
				},
			},
		},
	}
	writeTraj(t, dir, "t1", traj)

	loaded, err := LoadTrajectories(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := ComputeToolsMetrics(loaded)

	if m.TotalCalls != 4 {
		t.Errorf("TotalCalls = %d, want 4", m.TotalCalls)
	}
	if m.TotalResults != 2 {
		t.Errorf("TotalResults = %d, want 2", m.TotalResults)
	}
	if m.Wasted != 2 {
		t.Errorf("Wasted = %d, want 2 (c2 duplicate unobserved + c4 subagent no result)", m.Wasted)
	}
	if m.NestedAgentCalls != 1 {
		t.Errorf("NestedAgentCalls = %d, want 1", m.NestedAgentCalls)
	}
	// c2 repeats c1's (fn,args) → one duplicate occurrence.
	if m.Duplicates != 1 {
		t.Errorf("Duplicates = %d, want 1", m.Duplicates)
	}

	bash := m.ByTool["bash"]
	if bash.Calls != 2 || bash.Results != 1 || bash.Wasted != 1 {
		t.Errorf("bash stats = %+v, want calls=2 results=1 wasted=1", bash)
	}
	if bash.Duplicates != 1 {
		t.Errorf("bash duplicates = %d, want 1", bash.Duplicates)
	}
	if bash.AvgResultBytes != 2 {
		t.Errorf("bash AvgResultBytes = %d, want 2 (\"ok\")", bash.AvgResultBytes)
	}
	if bash.AvgLatencyMs < 450 || bash.AvgLatencyMs > 550 {
		t.Errorf("bash AvgLatencyMs = %d, want ~500", bash.AvgLatencyMs)
	}

	edit := m.ByTool["edit"]
	if edit.Errors != 1 {
		t.Errorf("edit errors = %d, want 1 (error content)", edit.Errors)
	}

	// Deterministic tool order.
	names := make([]string, 0, len(m.ByTool))
	for name := range m.ByTool {
		names = append(names, name)
	}
	sort.Strings(names)
	if names[0] != "bash" || names[1] != "edit" || names[2] != "subagent" {
		t.Errorf("tool order = %v, want [bash edit subagent]", names)
	}
}

func TestDiffGolden(t *testing.T) {
	produced := t.TempDir()
	golden := t.TempDir()

	write := func(dir, name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(produced, "add.go", "package artifacts\n")
	write(golden, "add.go", "package artifacts\n")

	write(produced, "add_test.go", "package artifacts\n\nfunc TestAdd(t *testing.T) {}\n")
	write(golden, "add_test.go", "package artifacts\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(1, 2); got != 3 {\n\t\tt.Fatal(got)\n\t}\n}\n")

	write(golden, "go.mod", "module evalartifacts\n\ngo 1.26\n")
	// produced go.mod intentionally missing.

	checks, pass := DiffGolden(produced, golden, []string{"go.mod", "add.go", "add_test.go"})
	if pass {
		t.Errorf("DiffGolden pass = true, want false (go.mod missing, add_test differs)")
	}
	byName := map[string]GoldenFile{}
	for _, c := range checks {
		byName[c.Name] = c
	}
	if byName["go.mod"].Match {
		t.Errorf("go.mod reported match, want mismatch (missing)")
	}
	if !byName["add.go"].Match {
		t.Errorf("add.go reported mismatch, want match")
	}
	if byName["add_test.go"].Match {
		t.Errorf("add_test.go reported match, want mismatch")
	}
	if byName["add_test.go"].Diff == "" {
		t.Errorf("add_test.go diff snippet is empty")
	}

	// All matching → pass.
	write(produced, "go.mod", "module evalartifacts\n\ngo 1.26\n")
	write(produced, "add_test.go", "package artifacts\n\nfunc TestAdd(t *testing.T) {\n\tif got := Add(1, 2); got != 3 {\n\t\tt.Fatal(got)\n\t}\n}\n")
	if _, pass := DiffGolden(produced, golden, []string{"go.mod", "add.go", "add_test.go"}); !pass {
		t.Errorf("DiffGolden pass = false, want true for identical files")
	}
}

func TestComputeTokenMetrics(t *testing.T) {
	dir := t.TempDir()

	// Two sessions with events.jsonl usage blocks; one session with no events.
	writeEvents := func(sessionID string, lines ...string) {
		t.Helper()
		sd := filepath.Join(dir, sessionID)
		if err := os.MkdirAll(sd, 0o755); err != nil {
			t.Fatal(err)
		}
		var b strings.Builder
		for _, l := range lines {
			b.WriteString(l)
			b.WriteString("\n")
		}
		if err := os.WriteFile(filepath.Join(sd, "events.jsonl"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeEvents("sess-a",
		`{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20}}`,
		`{"usageMetadata":{"promptTokenCount":50,"candidatesTokenCount":10}}`,
		`{"Content":{"parts":[]}}`, // no usage block — ignored
	)
	writeEvents("sess-b",
		`{"usageMetadata":{"promptTokenCount":30,"candidatesTokenCount":5,"cachedContentTokenCount":7}}`,
	)
	writeTraj(t, dir, "sess-a", baseTraj("sess-a", "pi-go"))
	writeTraj(t, dir, "sess-b", baseTraj("sess-b", "pi-go"))
	writeTraj(t, dir, "sess-c", baseTraj("sess-c", "pi-go")) // no events.jsonl

	loaded, err := LoadTrajectories(dir)
	if err != nil {
		t.Fatalf("LoadTrajectories: %v", err)
	}
	m := ComputeTokenMetrics(loaded)

	if m.PromptTokens != 180 {
		t.Errorf("PromptTokens = %d, want 180", m.PromptTokens)
	}
	if m.CompletionTokens != 35 {
		t.Errorf("CompletionTokens = %d, want 35", m.CompletionTokens)
	}
	if m.CachedTokens != 7 {
		t.Errorf("CachedTokens = %d, want 7", m.CachedTokens)
	}
	if m.TotalTokens != 215 {
		t.Errorf("TotalTokens = %d, want 215", m.TotalTokens)
	}
	if len(m.Sessions) != 2 {
		t.Errorf("len(Sessions) = %d, want 2 (sess-c has no usage)", len(m.Sessions))
	}
	byID := map[string]SessionTokenUsage{}
	for _, s := range m.Sessions {
		byID[s.SessionID] = s
	}
	if a := byID["sess-a"]; a.PromptTokens != 150 || a.CompletionTokens != 30 {
		t.Errorf("sess-a usage = %+v, want prompt 150 completion 30", a)
	}
	if b := byID["sess-b"]; b.PromptTokens != 30 || b.CompletionTokens != 5 || b.CachedTokens != 7 {
		t.Errorf("sess-b usage = %+v, want prompt 30 completion 5 cached 7", b)
	}
}
