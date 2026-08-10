package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/atif"
)

func TestParseJudgeVerdict_CleanJSON(t *testing.T) {
	reply := `{
  "scores": [
    {"dimension": "outcome_correctness", "score": 5, "rationale": "merged and matched golden"},
    {"dimension": "trajectory_quality", "score": 4, "rationale": "direct"},
    {"dimension": "concurrency_use", "score": 3, "rationale": "serial but small task"},
    {"dimension": "tools_efficiency", "score": 4, "rationale": "few duplicates"}
  ],
  "verdict": "pass",
  "summary": "A clean run.",
  "issues": ["three duplicate ls calls"]
}`

	v, err := ParseJudgeVerdict(reply)
	if err != nil {
		t.Fatalf("ParseJudgeVerdict: %v", err)
	}
	if v.Verdict != "pass" {
		t.Errorf("verdict = %q, want pass", v.Verdict)
	}
	if want := 4.0; v.Overall != want {
		t.Errorf("overall = %v, want %v", v.Overall, want)
	}
	if len(v.Issues) != 1 {
		t.Errorf("issues = %v, want the one issue", v.Issues)
	}
}

// Models routinely wrap the JSON in a code fence or prose despite being told
// not to; the parser must still find the object.
func TestParseJudgeVerdict_FencedAndProseWrapped(t *testing.T) {
	tests := []struct {
		name  string
		reply string
	}{
		{"code fence", "```json\n{\"scores\":[{\"dimension\":\"d\",\"score\":3,\"rationale\":\"r\"}],\"verdict\":\"pass\"}\n```"},
		{"prose before and after", "Here is my assessment:\n{\"scores\":[{\"dimension\":\"d\",\"score\":3,\"rationale\":\"r\"}],\"verdict\":\"pass\"}\nHope that helps!"},
		{"brace inside a string", `{"scores":[{"dimension":"d","score":3,"rationale":"saw a {literal brace}"}],"verdict":"pass"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := ParseJudgeVerdict(tt.reply)
			if err != nil {
				t.Fatalf("ParseJudgeVerdict: %v", err)
			}
			if v.Verdict != "pass" {
				t.Errorf("verdict = %q, want pass", v.Verdict)
			}
		})
	}
}

// An out-of-range score is clamped rather than rejected: it is still a usable
// signal, and losing the whole verdict over one bad number is worse.
func TestParseJudgeVerdict_ClampsScores(t *testing.T) {
	reply := `{"scores":[{"dimension":"a","score":9,"rationale":"r"},{"dimension":"b","score":-3,"rationale":"r"}],"verdict":"pass"}`

	v, err := ParseJudgeVerdict(reply)
	if err != nil {
		t.Fatalf("ParseJudgeVerdict: %v", err)
	}
	if v.Scores[0].Score != 5 {
		t.Errorf("high score = %d, want clamped to 5", v.Scores[0].Score)
	}
	if v.Scores[1].Score != 1 {
		t.Errorf("low score = %d, want clamped to 1", v.Scores[1].Score)
	}
	if want := 3.0; v.Overall != want {
		t.Errorf("overall = %v, want %v", v.Overall, want)
	}
}

// A missing or junk verdict is derived from the scores rather than left blank,
// so the field always means something to a reader of the report.
func TestParseJudgeVerdict_DerivesMissingVerdict(t *testing.T) {
	tests := []struct {
		name  string
		reply string
		want  string
	}{
		{"no verdict, good scores", `{"scores":[{"dimension":"a","score":4,"rationale":"r"}]}`, "pass"},
		{"no verdict, a 1 disqualifies", `{"scores":[{"dimension":"a","score":1,"rationale":"r"},{"dimension":"b","score":5,"rationale":"r"}]}`, "fail"},
		{"no verdict, mean below 3", `{"scores":[{"dimension":"a","score":2,"rationale":"r"},{"dimension":"b","score":3,"rationale":"r"}]}`, "fail"},
		{"junk verdict", `{"scores":[{"dimension":"a","score":4,"rationale":"r"}],"verdict":"maybe?"}`, "pass"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := ParseJudgeVerdict(tt.reply)
			if err != nil {
				t.Fatalf("ParseJudgeVerdict: %v", err)
			}
			if v.Verdict != tt.want {
				t.Errorf("verdict = %q, want %q", v.Verdict, tt.want)
			}
		})
	}
}

func TestParseJudgeVerdict_Rejects(t *testing.T) {
	tests := []struct {
		name  string
		reply string
	}{
		{"no JSON at all", "I cannot grade this run."},
		{"unbalanced object", `{"scores": [`},
		{"no scores", `{"verdict": "pass", "summary": "fine"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseJudgeVerdict(tt.reply); err == nil {
				t.Error("expected an error")
			}
		})
	}
}

// A judge that cannot run must degrade the report, not fail the eval: every
// failure comes back as a verdict carrying Error.
func TestJudge_FailuresBecomeVerdictErrors(t *testing.T) {
	report := &RunReport{Metadata: ReportMetadata{Spec: "s"}}

	t.Run("no completion func", func(t *testing.T) {
		v := Judge(context.Background(), nil, "some-model", report, "")
		if v.Error == "" {
			t.Error("expected an error verdict with no judge configured")
		}
	})

	t.Run("transport error", func(t *testing.T) {
		fail := func(context.Context, string, string) (string, error) {
			return "", errors.New("connection refused")
		}
		v := Judge(context.Background(), fail, "some-model", report, "")
		if !strings.Contains(v.Error, "connection refused") {
			t.Errorf("error = %q, does not carry the transport failure", v.Error)
		}
	})

	t.Run("unparseable reply", func(t *testing.T) {
		junk := func(context.Context, string, string) (string, error) {
			return "no json here", nil
		}
		v := Judge(context.Background(), junk, "some-model", report, "")
		if v.Error == "" {
			t.Error("expected an error verdict for an unparseable reply")
		}
	})
}

func TestJudge_RecordsModelAndScores(t *testing.T) {
	complete := func(_ context.Context, system, user string) (string, error) {
		if !strings.Contains(system, "outcome_correctness") {
			return "", errors.New("system prompt does not name the graded dimensions")
		}
		if !strings.Contains(user, "final phase") {
			return "", errors.New("user prompt does not carry the run outcome")
		}
		return `{"scores":[{"dimension":"outcome_correctness","score":5,"rationale":"clean"}],"verdict":"pass","summary":"good"}`, nil
	}

	v := Judge(context.Background(), complete, "judge-model", &RunReport{
		Outcome: RunOutcome{FinalPhase: "done", GoldenPass: true},
	}, "")
	if v.Error != "" {
		t.Fatalf("unexpected error: %s", v.Error)
	}
	if v.Model != "judge-model" {
		t.Errorf("model = %q, want judge-model", v.Model)
	}
	if v.Overall != 5 {
		t.Errorf("overall = %v, want 5", v.Overall)
	}
}

func TestBuildJudgePrompt_CarriesTheEvidence(t *testing.T) {
	r := &RunReport{
		Metadata: ReportMetadata{Spec: "eval-orchestrator", Mode: "parallel", Model: "m"},
		Outcome: RunOutcome{
			FinalPhase:  "failed",
			Retries:     2,
			Reason:      "**Merge failed**\nfor spec",
			GateResults: []GateResult{{Name: "test", Command: "go test ./...", Passed: false}},
			GoldenCheck: []GoldenFile{{Name: "add.go", Match: false}},
		},
		Trajectory:  TrajectoryMetrics{TotalSteps: 40, TotalToolCalls: 25, MaxDepth: 2},
		Concurrency: ConcurrencyMetrics{PoolBudget: 3, MaxRunning: 2},
		Tools: ToolsMetrics{
			TotalCalls: 25,
			Duplicates: 4,
			ByTool:     map[string]ToolStats{"bash": {Calls: 10, Errors: 2}},
		},
	}

	got := BuildJudgePrompt(r, "1. bash(go test) -> ERROR: fail\n")

	for _, want := range []string{
		"eval-orchestrator", "parallel",
		"final phase: failed",
		"retries: 2",
		"Merge failed",
		"gate \"test\"",
		"golden mismatch: add.go",
		"total tool calls: 25",
		"max nesting depth: 2",
		"pool budget: 3",
		"duplicate calls (same tool, same arguments): 4",
		"bash",
		"Tool-call timeline",
		"ERROR: fail",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt is missing %q\n---\n%s", want, got)
		}
	}
}

// The reason field can hold a multi-line merge error; it must not break the
// prompt's line-oriented structure.
func TestBuildJudgePrompt_FlattensMultilineReason(t *testing.T) {
	r := &RunReport{Outcome: RunOutcome{Reason: "line one\nline two\nline three"}}

	got := BuildJudgePrompt(r, "")

	if !strings.Contains(got, "failure reason: line one line two line three") {
		t.Errorf("multi-line reason was not flattened:\n%s", got)
	}
}

func TestBuildJudgePrompt_NilReport(t *testing.T) {
	if got := BuildJudgePrompt(nil, "some digest"); !strings.Contains(got, "some digest") {
		t.Errorf("nil report dropped the digest: %q", got)
	}
}

func TestTrajectoryDigest(t *testing.T) {
	loaded := []*LoadedTrajectory{{
		SessionID: "260810-1601-abcde-12345",
		Traj: &atif.Trajectory{
			SessionID: "260810-1601-abcde-12345",
			Agent:     atif.AgentInfo{Name: "pi-go"},
			Steps: []atif.Step{
				{
					ToolCalls: []atif.ToolCall{
						{ToolCallID: "c1", FunctionName: "bash", Arguments: map[string]any{"command": "go test ./..."}},
						{ToolCallID: "c2", FunctionName: "read", Arguments: map[string]any{"path": "add.go"}},
						{ToolCallID: "c3", FunctionName: "subagent", Arguments: map[string]any{"task": "write tests"}},
						{ToolCallID: "c4", FunctionName: "write", Arguments: map[string]any{"path": "x.go"}},
					},
				},
				{
					Observation: &atif.Observation{Results: []atif.ObservationResult{
						{SourceCallID: "c1", Content: "error: build failed"},
						{SourceCallID: "c2", Content: "package main"},
						{SourceCallID: "c3", Content: "done", SubagentTrajectoryRef: "sess-2"},
					}},
				},
			},
		},
	}}

	got := TrajectoryDigest(loaded, 60)

	for _, want := range []string{
		"agent pi-go",
		"bash(",
		"ERROR: error: build failed",
		"read(",
		"[spawned subagent]",
		"write(", // c4 has no matching observation
		"(no result)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("digest is missing %q\n---\n%s", want, got)
		}
	}
}

// One runaway session must not crowd the others out of the judge's prompt.
func TestTrajectoryDigest_CapsCallsPerSession(t *testing.T) {
	var calls []atif.ToolCall
	for i := range 50 {
		calls = append(calls, atif.ToolCall{ToolCallID: string(rune('a' + i%26)), FunctionName: "bash"})
	}
	loaded := []*LoadedTrajectory{{
		Traj: &atif.Trajectory{Steps: []atif.Step{{ToolCalls: calls}}},
	}}

	got := TrajectoryDigest(loaded, 5)

	if strings.Count(got, "bash(") != 5 {
		t.Errorf("digest has %d calls, want the 5-call cap\n%s", strings.Count(got, "bash("), got)
	}
	if !strings.Contains(got, "more calls omitted") {
		t.Errorf("digest does not disclose the truncation:\n%s", got)
	}
}

func TestTrajectoryDigest_EmptyAndNil(t *testing.T) {
	if got := TrajectoryDigest(nil, 10); got != "" {
		t.Errorf("digest of no trajectories = %q, want empty", got)
	}
	if got := TrajectoryDigest([]*LoadedTrajectory{nil, {Traj: nil}}, 10); got != "" {
		t.Errorf("digest of nil trajectories = %q, want empty", got)
	}
	got := TrajectoryDigest([]*LoadedTrajectory{{Traj: &atif.Trajectory{SessionID: "s"}}}, 10)
	if !strings.Contains(got, "(no tool calls)") {
		t.Errorf("digest of a callless session = %q", got)
	}
}
