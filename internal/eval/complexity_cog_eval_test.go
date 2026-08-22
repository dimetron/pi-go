package eval

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/atif"
)

// The literals in this file were captured by running these exact fixtures
// through the pre-refactor source (a `git archive HEAD` copy in a scratch
// directory), so they pin the digest, prompt and metric output the judge and
// the recorded eval reports depend on. A shift in shape or content would make
// new runs non-comparable with old ones without failing anything, which is
// precisely what these tests exist to prevent.

// cogDigestFixture exercises every branch of one digest session: a nil entry,
// an entry with no trajectory, a sized result, an error result, a call with no
// result, a subagent-spawning result, an orphaned observation, over-long
// arguments, and a session with no tool calls at all.
func cogDigestFixture() []*LoadedTrajectory {
	long := ""
	for i := 0; i < 40; i++ {
		long += "abcdefg-"
	}
	parent := &atif.Trajectory{
		SessionID: "260809-0249-c53d2-7561f",
		Agent:     atif.AgentInfo{Name: "pi-go", ModelName: "opus"},
		Steps: []atif.Step{
			{
				StepID:    1,
				Timestamp: "2026-08-22T10:00:00Z",
				ToolCalls: []atif.ToolCall{
					{ToolCallID: "c1", FunctionName: "bash", Arguments: map[string]any{"cmd": "ls"}},
					{ToolCallID: "c2", FunctionName: "read", Arguments: map[string]any{"path": long}},
					{ToolCallID: "c3", FunctionName: "grep"},
					{ToolCallID: "c4", FunctionName: "subagent", Arguments: map[string]any{"task": "x"}},
				},
				Observation: &atif.Observation{Results: []atif.ObservationResult{
					{SourceCallID: "c1", Content: "hello world"},
					{SourceCallID: "c2", Content: "error: no such\nfile or\tdirectory"},
					{SourceCallID: "", Content: "orphan"},
					{SourceCallID: "c4", Content: map[string]any{"ok": true}, SubagentTrajectoryRef: "/s/child/trajectory.atif.json"},
				}},
			},
			{StepID: 2, ToolCalls: []atif.ToolCall{{ToolCallID: "c5", FunctionName: "write"}}},
		},
	}
	empty := &atif.Trajectory{SessionID: "sess-empty", Agent: atif.AgentInfo{Name: "worker"}}
	return []*LoadedTrajectory{
		nil,
		{SessionID: "nil-traj"},
		{SessionID: parent.SessionID, Traj: parent},
		{SessionID: "sess-empty", Traj: empty},
	}
}

// cogCappedFixture has seven calls spread over two steps, so a cap of five
// stops mid-step and the remainder is reported as a count.
func cogCappedFixture() []*LoadedTrajectory {
	var first, second []atif.ToolCall
	for i := 0; i < 3; i++ {
		first = append(first, atif.ToolCall{ToolCallID: fmt.Sprintf("a%d", i), FunctionName: "bash"})
	}
	for i := 0; i < 4; i++ {
		second = append(second, atif.ToolCall{ToolCallID: fmt.Sprintf("b%d", i), FunctionName: "read"})
	}
	t := &atif.Trajectory{
		SessionID: "capped-session-id",
		Agent:     atif.AgentInfo{Name: "pi-go"},
		Steps: []atif.Step{
			{StepID: 1, ToolCalls: first, Observation: &atif.Observation{Results: []atif.ObservationResult{{SourceCallID: "a0", Content: "ok"}}}},
			{StepID: 2, ToolCalls: second},
		},
	}
	return []*LoadedTrajectory{{SessionID: t.SessionID, Traj: t}}
}

// cogToolsFixture covers observed and unobserved calls, an error result, a
// structured result, an orphaned observation, a call with no ID, duplicate
// argument sets across sessions, a subagent call, and an unparseable step
// timestamp (which must leave latency out of the average).
func cogToolsFixture() []*LoadedTrajectory {
	parent := &atif.Trajectory{
		SessionID: "p",
		Agent:     atif.AgentInfo{Name: "pi-go", ModelName: "m1"},
		Steps: []atif.Step{
			{
				StepID:    1,
				Timestamp: "2026-08-22T10:00:00Z",
				ToolCalls: []atif.ToolCall{
					{ToolCallID: "t1", FunctionName: "bash", Arguments: map[string]any{"cmd": "ls"}},
					{ToolCallID: "t2", FunctionName: "bash", Arguments: map[string]any{"cmd": "ls"}},
					{FunctionName: "read", Arguments: map[string]any{"p": "a"}},
					{ToolCallID: "t4", FunctionName: "subagent"},
				},
			},
			{
				StepID:    2,
				Timestamp: "2026-08-22T10:00:03Z",
				Observation: &atif.Observation{Results: []atif.ObservationResult{
					{SourceCallID: "t1", Content: "aaaa"},
					{SourceCallID: "t2", Content: "error: boom"},
					{SourceCallID: "unknown", Content: "x"},
					{SourceCallID: "t4", Content: map[string]any{"k": "v"}, SubagentTrajectoryRef: "/s/c/trajectory.atif.json"},
				}},
			},
			{
				StepID:    3,
				Timestamp: "bogus",
				ToolCalls: []atif.ToolCall{{ToolCallID: "t5", FunctionName: "bash", Arguments: map[string]any{"cmd": "ls"}}},
			},
		},
	}
	child := &atif.Trajectory{
		SessionID: "c",
		Agent:     atif.AgentInfo{Name: "worker", ModelName: "m2"},
		Steps: []atif.Step{
			{
				StepID:      1,
				Timestamp:   "2026-08-22T10:00:01Z",
				ToolCalls:   []atif.ToolCall{{ToolCallID: "u1", FunctionName: "read", Arguments: map[string]any{"p": "a"}}},
				Observation: &atif.Observation{Results: []atif.ObservationResult{{SourceCallID: "u1", Content: "bb"}}},
			},
		},
	}
	return []*LoadedTrajectory{{SessionID: "p", Traj: parent}, {SessionID: "c", Traj: child}}
}

// cogChainFixture is a three-level chain where the leaf is referenced by both
// the root and the middle session, so its depth must come from the deeper
// parent rather than the first one visited.
func cogChainFixture() []*LoadedTrajectory {
	root := &atif.Trajectory{
		SessionID: "root",
		Agent:     atif.AgentInfo{Name: "pi-go", ModelName: "m1"},
		Steps: []atif.Step{
			{
				StepID:    1,
				Timestamp: "2026-08-22T10:00:00Z",
				ToolCalls: []atif.ToolCall{{ToolCallID: "r1", FunctionName: "subagent"}},
				Observation: &atif.Observation{Results: []atif.ObservationResult{
					{SourceCallID: "r1", SubagentTrajectoryRef: "/s/mid/trajectory.atif.json"},
					{SourceCallID: "r1", SubagentTrajectoryRef: "/s/leaf/trajectory.atif.json"},
				}},
			},
			{StepID: 2, Timestamp: "2026-08-22T10:00:30Z"},
		},
	}
	mid := &atif.Trajectory{
		SessionID: "mid",
		Agent:     atif.AgentInfo{Name: "mid-agent", ModelName: "m2"},
		Steps: []atif.Step{
			{
				StepID:      1,
				ToolCalls:   []atif.ToolCall{{ToolCallID: "m1", FunctionName: "subagent"}},
				Observation: &atif.Observation{Results: []atif.ObservationResult{{SourceCallID: "m1", SubagentTrajectoryRef: "/s/leaf/trajectory.atif.json"}}},
			},
		},
		ContinuedTrajectoryRef: "/s/prev/trajectory.atif.json",
	}
	leaf := &atif.Trajectory{
		SessionID: "leaf",
		Agent:     atif.AgentInfo{Name: "leaf-agent"},
		Steps:     []atif.Step{{StepID: 1, Timestamp: "bad", ToolCalls: []atif.ToolCall{{ToolCallID: "l1", FunctionName: "bash"}}}},
	}
	return []*LoadedTrajectory{
		{SessionID: "root", Traj: root},
		{SessionID: "mid", Traj: mid},
		{SessionID: "leaf", Traj: leaf},
	}
}

// cogCycleFixture is two sessions that reference each other, which the depth
// walk must survive rather than recurse forever on.
func cogCycleFixture() []*LoadedTrajectory {
	a := &atif.Trajectory{
		SessionID: "a", Agent: atif.AgentInfo{Name: "A"},
		Steps: []atif.Step{{StepID: 1, Observation: &atif.Observation{Results: []atif.ObservationResult{
			{SourceCallID: "x", SubagentTrajectoryRef: "/s/b/trajectory.atif.json"},
		}}}},
	}
	b := &atif.Trajectory{
		SessionID: "b", Agent: atif.AgentInfo{Name: "B"},
		Steps: []atif.Step{{StepID: 1, Observation: &atif.Observation{Results: []atif.ObservationResult{
			{SourceCallID: "y", SubagentTrajectoryRef: "/s/a/trajectory.atif.json"},
		}}}},
	}
	return []*LoadedTrajectory{{SessionID: "a", Traj: a}, {SessionID: "b", Traj: b}}
}

func cogJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestTrajectoryDigest_PinnedOutput(t *testing.T) {
	tests := []struct {
		name     string
		loaded   []*LoadedTrajectory
		maxCalls int
		want     string
	}{
		{
			name:     "every result shape, default cap",
			loaded:   cogDigestFixture(),
			maxCalls: 0,
			want: "### session 260809-0249-… (agent pi-go)\n" +
				"1. bash({\"cmd\":\"ls\"}) -> 11 bytes\n" +
				"2. read({\"path\":\"abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg-abcdefg…) -> ERROR: error: no such file or directory\n" +
				"3. grep() -> (no result)\n" +
				"4. subagent({\"task\":\"x\"}) -> 11 bytes [spawned subagent]\n" +
				"5. write() -> (no result)\n" +
				"\n" +
				"### session sess-empty (agent worker)\n" +
				"(no tool calls)\n" +
				"\n",
		},
		{
			name:     "cap trips mid-step",
			loaded:   cogCappedFixture(),
			maxCalls: 5,
			want: "### session capped-sessi… (agent pi-go)\n" +
				"1. bash() -> 2 bytes\n" +
				"2. bash() -> (no result)\n" +
				"3. bash() -> (no result)\n" +
				"4. read() -> (no result)\n" +
				"5. read() -> (no result)\n" +
				"... (2 more calls omitted)\n" +
				"\n",
		},
		{name: "nothing loaded", loaded: nil, maxCalls: 10, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TrajectoryDigest(tt.loaded, tt.maxCalls); got != tt.want {
				t.Errorf("TrajectoryDigest mismatch\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestTrajectoryDigest_DefaultCapIsSixty pins the substitution the guard makes
// for a non-positive cap, which is otherwise invisible in the output.
func TestTrajectoryDigest_DefaultCapIsSixty(t *testing.T) {
	var calls []atif.ToolCall
	for i := 0; i < 65; i++ {
		calls = append(calls, atif.ToolCall{ToolCallID: fmt.Sprintf("c%d", i), FunctionName: "bash"})
	}
	loaded := []*LoadedTrajectory{{SessionID: "s", Traj: &atif.Trajectory{SessionID: "s", Steps: []atif.Step{{ToolCalls: calls}}}}}

	for _, maxCalls := range []int{0, -1} {
		got := TrajectoryDigest(loaded, maxCalls)
		if want := "60. bash() -> (no result)\n... (5 more calls omitted)\n"; !strings.Contains(got, want) {
			t.Errorf("maxCalls=%d: want tail %q in\n%s", maxCalls, want, got)
		}
		if strings.Contains(got, "61. bash()") {
			t.Errorf("maxCalls=%d: emitted a 61st call", maxCalls)
		}
	}
}

func TestComputeToolsMetrics_PinnedOutput(t *testing.T) {
	want := `{
  "total_calls": 6,
  "total_results": 4,
  "wasted": 2,
  "duplicates": 3,
  "nested_agent_calls": 1,
  "by_tool": {
    "bash": {
      "calls": 3,
      "results": 2,
      "errors": 1,
      "wasted": 1,
      "duplicates": 2,
      "avg_result_bytes": 7,
      "avg_latency_ms": 3000
    },
    "read": {
      "calls": 2,
      "results": 1,
      "errors": 0,
      "wasted": 1,
      "duplicates": 1,
      "avg_result_bytes": 2,
      "avg_latency_ms": 0
    },
    "subagent": {
      "calls": 1,
      "results": 1,
      "errors": 0,
      "wasted": 0,
      "duplicates": 0,
      "avg_result_bytes": 9,
      "avg_latency_ms": 3000
    }
  }
}`
	if got := cogJSON(t, ComputeToolsMetrics(cogToolsFixture())); got != want {
		t.Errorf("ComputeToolsMetrics mismatch\n got: %s\nwant: %s", got, want)
	}
}

// TestComputeToolsMetrics_StableAcrossRuns guards the ordering rebuild: the
// same input must serialize identically every time, not merely be equal.
func TestComputeToolsMetrics_StableAcrossRuns(t *testing.T) {
	first := cogJSON(t, ComputeToolsMetrics(cogToolsFixture()))
	for i := 0; i < 20; i++ {
		if got := cogJSON(t, ComputeToolsMetrics(cogToolsFixture())); got != first {
			t.Fatalf("run %d differs from run 0\n got: %s\nwant: %s", i, got, first)
		}
	}
}

func TestComputeToolsMetrics_Empty(t *testing.T) {
	want := `{
  "total_calls": 0,
  "total_results": 0,
  "wasted": 0,
  "duplicates": 0,
  "nested_agent_calls": 0,
  "by_tool": {}
}`
	if got := cogJSON(t, ComputeToolsMetrics(nil)); got != want {
		t.Errorf("ComputeToolsMetrics(nil) mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestComputeTrajectoryMetrics_PinnedOutput(t *testing.T) {
	tests := []struct {
		name   string
		loaded []*LoadedTrajectory
		want   string
	}{
		{
			name:   "three-level chain, leaf reached from two depths",
			loaded: cogChainFixture(),
			want: `{
  "sessions": [
    {
      "session_id": "root",
      "agent_name": "pi-go",
      "model": "m1",
      "steps": 2,
      "tool_calls": 1,
      "subagent_refs": 2,
      "depth": 0,
      "started_at": "2026-08-22T10:00:00Z",
      "duration": "30s"
    },
    {
      "session_id": "mid",
      "agent_name": "mid-agent",
      "model": "m2",
      "steps": 1,
      "tool_calls": 1,
      "subagent_refs": 1,
      "depth": 1,
      "continued_from": "/s/prev/trajectory.atif.json"
    },
    {
      "session_id": "leaf",
      "agent_name": "leaf-agent",
      "model": "",
      "steps": 1,
      "tool_calls": 1,
      "subagent_refs": 0,
      "depth": 2
    }
  ],
  "total_steps": 4,
  "total_tool_calls": 3,
  "nested_agent_calls": 3,
  "max_depth": 2
}`,
		},
		{
			name:   "mutual reference does not hang",
			loaded: cogCycleFixture(),
			want: `{
  "sessions": [
    {
      "session_id": "b",
      "agent_name": "B",
      "model": "",
      "steps": 1,
      "tool_calls": 0,
      "subagent_refs": 1,
      "depth": 1
    },
    {
      "session_id": "a",
      "agent_name": "A",
      "model": "",
      "steps": 1,
      "tool_calls": 0,
      "subagent_refs": 1,
      "depth": 2
    }
  ],
  "total_steps": 2,
  "total_tool_calls": 0,
  "nested_agent_calls": 2,
  "max_depth": 2
}`,
		},
		{
			name:   "nothing loaded",
			loaded: nil,
			want: `{
  "sessions": null,
  "total_steps": 0,
  "total_tool_calls": 0,
  "nested_agent_calls": 0,
  "max_depth": 0
}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			done := make(chan string, 1)
			go func() { done <- cogJSON(t, ComputeTrajectoryMetrics(tt.loaded)) }()
			select {
			case got := <-done:
				if got != tt.want {
					t.Errorf("ComputeTrajectoryMetrics mismatch\n got: %s\nwant: %s", got, tt.want)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("ComputeTrajectoryMetrics did not return; the cycle guard regressed")
			}
		})
	}
}

func cogReport() *RunReport {
	return &RunReport{
		Metadata: ReportMetadata{Spec: "spec.md", Mode: "parallel", Model: "opus", Timestamp: time.Unix(0, 0).UTC()},
		Outcome: RunOutcome{
			FinalPhase:  "failed",
			Retries:     2,
			GoldenPass:  false,
			Reason:      "Verification failed:\n  gate build\n  gate test",
			BaselineRef: "v1.2.3",
			GateResults: []GateResult{
				{Name: "build", Command: "go build ./...", Passed: true},
				{Name: "test", Command: "go test ./...", Passed: false},
			},
			GoldenCheck: []GoldenFile{{Name: "add.go", Match: true}, {Name: "add_test.go", Match: false}},
		},
		Trajectory:  ComputeTrajectoryMetrics(cogChainFixture()),
		Concurrency: ComputeConcurrencyMetrics(4, []ConcurrencySample{{Running: 1}, {Running: 3}}),
		Tools:       ComputeToolsMetrics(cogToolsFixture()),
	}
}

func TestBuildJudgePrompt_PinnedOutput(t *testing.T) {
	want := "# Run under review\n\nSpec: spec.md (mode: parallel, model: opus)\n\n## Outcome\n\n" +
		"- final phase: failed\n- retries: 2\n- golden artifacts match: false\n" +
		"- matches baseline v1.2.3: false\n" +
		"- gate \"build\" (go build ./...): passed=true\n" +
		"- gate \"test\" (go test ./...): passed=false\n" +
		"- golden mismatch: add_test.go\n" +
		"- failure reason: Verification failed: gate build gate test\n" +
		"\n## Trajectory\n\n- sessions: 3\n- total steps: 4\n- total tool calls: 3\n" +
		"- nested agent calls: 3\n- max nesting depth: 2\n" +
		"\n## Concurrency\n\n- pool budget: 4 (nested worker budget: 2)\n" +
		"- max concurrent agents: 3\n- mean concurrent agents: 2.00\n" +
		"- fraction of time with >1 running: 0.50\n" +
		"\n## Tools\n\n- total calls: 6 (results: 4)\n- calls with no result: 2\n" +
		"- duplicate calls (same tool, same arguments): 3\n" +
		"\n| tool | calls | errors | wasted | duplicates | avg result bytes |\n" +
		"|---|---|---|---|---|---|\n" +
		"| bash | 3 | 1 | 1 | 2 | 7 |\n" +
		"| read | 2 | 0 | 1 | 1 | 2 |\n" +
		"| subagent | 1 | 0 | 0 | 0 | 9 |\n" +
		"\n## Tool-call timeline\n\n" +
		TrajectoryDigest(cogDigestFixture(), 0) + "\n"

	if got := BuildJudgePrompt(cogReport(), TrajectoryDigest(cogDigestFixture(), 0)); got != want {
		t.Errorf("BuildJudgePrompt mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildJudgePrompt_NilReportAndBlankDigest(t *testing.T) {
	if got, want := BuildJudgePrompt(nil, "  \n "), "# Run under review\n\n"; got != want {
		t.Errorf("nil report: got %q, want %q", got, want)
	}

	want := "# Run under review\n\nSpec:  (mode: , model: )\n\n## Outcome\n\n" +
		"- final phase: \n- retries: 0\n- golden artifacts match: false\n" +
		"\n## Trajectory\n\n- sessions: 0\n- total steps: 0\n- total tool calls: 0\n" +
		"- nested agent calls: 0\n- max nesting depth: 0\n" +
		"\n## Concurrency\n\n- pool budget: 0 (nested worker budget: 0)\n" +
		"- max concurrent agents: 0\n- mean concurrent agents: 0.00\n" +
		"- fraction of time with >1 running: 0.00\n" +
		"\n## Tools\n\n- total calls: 0 (results: 0)\n- calls with no result: 0\n" +
		"- duplicate calls (same tool, same arguments): 0\n"
	if got := BuildJudgePrompt(&RunReport{}, ""); got != want {
		t.Errorf("empty report: got %q, want %q", got, want)
	}
}

func TestParseJudgeVerdict_PinnedOutcomes(t *testing.T) {
	tests := []cogVerdictCase{
		{
			name:        "stated verdict survives a clamped disqualifying score",
			reply:       `{"verdict":"PASS ","scores":[{"dimension":"a","score":9},{"dimension":"b","score":-4}],"summary":"s"}`,
			wantScores:  []int{5, 1},
			wantOverall: 3,
			wantVerdict: "pass",
		},
		{
			name:        "fenced reply, a score of 1 forces fail",
			reply:       "prose ```json\n{\"scores\":[{\"dimension\":\"a\",\"score\":1},{\"dimension\":\"b\",\"score\":5}]}\n``` tail",
			wantScores:  []int{1, 5},
			wantOverall: 3,
			wantVerdict: "fail",
		},
		{
			name:        "mean below three fails without any score of one",
			reply:       `{"scores":[{"dimension":"a","score":2},{"dimension":"b","score":3}]}`,
			wantScores:  []int{2, 3},
			wantOverall: 2.5,
			wantVerdict: "fail",
		},
		{
			name:        "healthy scores derive a pass",
			reply:       `{"scores":[{"dimension":"a","score":4},{"dimension":"b","score":5}]}`,
			wantScores:  []int{4, 5},
			wantOverall: 4.5,
			wantVerdict: "pass",
		},
		{
			name:        "unrecognized verdict is re-derived, not kept",
			reply:       `{"verdict":"maybe","scores":[{"dimension":"a","score":3},{"dimension":"b","score":3}]}`,
			wantScores:  []int{3, 3},
			wantOverall: 3,
			wantVerdict: "pass",
		},
		{name: "no object at all", reply: "no json here", wantErr: `judge reply contains no JSON object: "no json here"`},
		{name: "object with no scores", reply: `{"scores":[]}`, wantErr: "judge reply has no scores"},
		{
			name:    "scores of the wrong type",
			reply:   `{"scores": "oops"}`,
			wantErr: "parse judge reply: json: cannot unmarshal string into Go struct field JudgeVerdict.scores of type []eval.JudgeScore",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := ParseJudgeVerdict(tt.reply)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			checkVerdict(t, v, tt)
		})
	}
}

// cogVerdictCase is one reply and the verdict it must parse into.
type cogVerdictCase struct {
	name        string
	reply       string
	wantScores  []int
	wantOverall float64
	wantVerdict string
	wantErr     string
}

func checkVerdict(t *testing.T, v JudgeVerdict, tt cogVerdictCase) {
	t.Helper()
	if len(v.Scores) != len(tt.wantScores) {
		t.Fatalf("got %d scores, want %d", len(v.Scores), len(tt.wantScores))
	}
	for i, want := range tt.wantScores {
		if v.Scores[i].Score != want {
			t.Errorf("score %d = %d, want %d", i, v.Scores[i].Score, want)
		}
	}
	if v.Overall != tt.wantOverall {
		t.Errorf("overall = %v, want %v", v.Overall, tt.wantOverall)
	}
	if v.Verdict != tt.wantVerdict {
		t.Errorf("verdict = %q, want %q", v.Verdict, tt.wantVerdict)
	}
}
