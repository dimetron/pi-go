package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// --- Judge types ---

// JudgeScore is one graded dimension of the run.
type JudgeScore struct {
	Dimension string `json:"dimension"`
	Score     int    `json:"score"` // 1 (bad) .. 5 (excellent)
	Rationale string `json:"rationale"`
}

// JudgeVerdict is an LLM's assessment of a run. It grades the same three axes
// the harness measures numerically — trajectory, concurrency, tools — plus the
// run's outcome, because a run can score well on efficiency and still produce
// the wrong artifacts.
//
// The verdict is advisory: it is recorded in the report, never asserted on. An
// LLM grader is not a stable enough signal to fail a build, but it catches the
// qualitative regressions the counters miss (a worker that thrashed, retried
// the same failing command, or reached the answer by accident).
type JudgeVerdict struct {
	Model   string       `json:"model"`
	Scores  []JudgeScore `json:"scores"`
	Overall float64      `json:"overall"` // mean of Scores, 0 when none
	Verdict string       `json:"verdict"` // "pass" | "fail"
	Summary string       `json:"summary"`
	Issues  []string     `json:"issues,omitempty"`
	// Error is set when the judge could not be run or its reply could not be
	// parsed. The rest of the verdict is then zero.
	Error string `json:"error,omitempty"`
}

// JudgeDimensions are the axes the judge is asked to grade, in prompt order.
var JudgeDimensions = []string{
	"outcome_correctness",
	"trajectory_quality",
	"concurrency_use",
	"tools_efficiency",
}

// CompleteFunc is a single-shot LLM call: system prompt plus user prompt in,
// assistant text out. The eval package stays free of provider dependencies by
// taking one of these from the caller.
type CompleteFunc func(ctx context.Context, system, user string) (string, error)

const judgeSystemPrompt = `You are grading the execution trace of an autonomous coding agent, not the code it wrote.

You are given a metrics report and a condensed tool-call timeline from one run of a
"/run" orchestrator: a coordinator agent that spawns worker subagents in isolated git
worktrees, runs gate commands, verifies a plan checklist, and merges the result back.

Grade these dimensions from 1 (bad) to 5 (excellent):

- outcome_correctness: did the run reach its goal? Weigh the final phase, gate results
  and the golden-file diff most heavily. A run that failed to merge or produced
  mismatched artifacts cannot score above 2 here.
- trajectory_quality: was the path to the result direct and purposeful? Penalize
  aimless exploration, re-reading the same files, and work redone after a retry.
- concurrency_use: was subagent fan-out used appropriately for the task? Neither
  serializing work that could parallelize, nor spawning subagents for trivial steps.
- tools_efficiency: were tool calls economical? Penalize duplicate calls, calls whose
  results were never used, error-producing calls repeated without changing approach,
  and pulling far more content than needed.

Judge only what the evidence shows. If the timeline is too sparse to assess a
dimension, score it 3 and say so in the rationale.

Reply with ONLY a JSON object, no prose and no code fences:

{
  "scores": [
    {"dimension": "outcome_correctness", "score": 1-5, "rationale": "one or two sentences"},
    {"dimension": "trajectory_quality", "score": 1-5, "rationale": "..."},
    {"dimension": "concurrency_use", "score": 1-5, "rationale": "..."},
    {"dimension": "tools_efficiency", "score": 1-5, "rationale": "..."}
  ],
  "verdict": "pass" or "fail",
  "summary": "two or three sentences on how this run went",
  "issues": ["specific, actionable problem", "..."]
}

Set verdict to "fail" if the run did not achieve its goal or any dimension scores 1.`

// Judge asks an LLM to grade a run and returns its verdict. Any failure —
// transport, empty reply, unparseable JSON — comes back as a verdict carrying
// Error rather than as a Go error, so an unavailable judge degrades the report
// instead of failing the eval.
func Judge(ctx context.Context, complete CompleteFunc, model string, r *RunReport, digest string) JudgeVerdict {
	if complete == nil {
		return JudgeVerdict{Model: model, Error: "no judge model configured"}
	}
	reply, err := complete(ctx, judgeSystemPrompt, BuildJudgePrompt(r, digest))
	if err != nil {
		return JudgeVerdict{Model: model, Error: err.Error()}
	}
	v, err := ParseJudgeVerdict(reply)
	if err != nil {
		return JudgeVerdict{Model: model, Error: err.Error()}
	}
	v.Model = model
	return v
}

// BuildJudgePrompt renders the evidence the judge grades: the run's outcome and
// metrics, then the condensed tool-call timeline. It deliberately hands over
// the computed metrics rather than raw trajectories — the numbers are what the
// harness is confident about, and they fit in a prompt.
func BuildJudgePrompt(r *RunReport, digest string) string {
	var b strings.Builder

	b.WriteString("# Run under review\n\n")
	if r != nil {
		writeRunEvidence(&b, r)
	}

	if strings.TrimSpace(digest) != "" {
		b.WriteString("\n## Tool-call timeline\n\n")
		b.WriteString(digest)
		b.WriteString("\n")
	}

	return b.String()
}

// writeRunEvidence renders the report's four sections — outcome, trajectory,
// concurrency, tools — in the order the judge reads them.
func writeRunEvidence(b *strings.Builder, r *RunReport) {
	fmt.Fprintf(b, "Spec: %s (mode: %s, model: %s)\n\n", r.Metadata.Spec, r.Metadata.Mode, r.Metadata.Model)

	b.WriteString("## Outcome\n\n")
	fmt.Fprintf(b, "- final phase: %s\n", r.Outcome.FinalPhase)
	fmt.Fprintf(b, "- retries: %d\n", r.Outcome.Retries)
	fmt.Fprintf(b, "- golden artifacts match: %v\n", r.Outcome.GoldenPass)
	if r.Outcome.BaselineRef != "" {
		fmt.Fprintf(b, "- matches baseline %s: %v\n", r.Outcome.BaselineRef, r.Outcome.BaselinePass)
	}
	for _, g := range r.Outcome.GateResults {
		fmt.Fprintf(b, "- gate %q (%s): passed=%v\n", g.Name, g.Command, g.Passed)
	}
	for _, g := range r.Outcome.GoldenCheck {
		if !g.Match {
			fmt.Fprintf(b, "- golden mismatch: %s\n", g.Name)
		}
	}
	if r.Outcome.Reason != "" {
		fmt.Fprintf(b, "- failure reason: %s\n", truncate(oneLine(r.Outcome.Reason), 600))
	}

	b.WriteString("\n## Trajectory\n\n")
	fmt.Fprintf(b, "- sessions: %d\n", len(r.Trajectory.Sessions))
	fmt.Fprintf(b, "- total steps: %d\n", r.Trajectory.TotalSteps)
	fmt.Fprintf(b, "- total tool calls: %d\n", r.Trajectory.TotalToolCalls)
	fmt.Fprintf(b, "- nested agent calls: %d\n", r.Trajectory.NestedAgentCalls)
	fmt.Fprintf(b, "- max nesting depth: %d\n", r.Trajectory.MaxDepth)

	b.WriteString("\n## Concurrency\n\n")
	fmt.Fprintf(b, "- pool budget: %d (nested worker budget: %d)\n", r.Concurrency.PoolBudget, r.Concurrency.WorkerBudget)
	fmt.Fprintf(b, "- max concurrent agents: %d\n", r.Concurrency.MaxRunning)
	fmt.Fprintf(b, "- mean concurrent agents: %.2f\n", r.Concurrency.MeanRunning)
	fmt.Fprintf(b, "- fraction of time with >1 running: %.2f\n", r.Concurrency.ParallelOverlap)

	b.WriteString("\n## Tools\n\n")
	fmt.Fprintf(b, "- total calls: %d (results: %d)\n", r.Tools.TotalCalls, r.Tools.TotalResults)
	fmt.Fprintf(b, "- calls with no result: %d\n", r.Tools.Wasted)
	fmt.Fprintf(b, "- duplicate calls (same tool, same arguments): %d\n", r.Tools.Duplicates)
	if len(r.Tools.ByTool) > 0 {
		b.WriteString("\n| tool | calls | errors | wasted | duplicates | avg result bytes |\n")
		b.WriteString("|---|---|---|---|---|---|\n")
		for _, name := range sortedKeys(r.Tools.ByTool) {
			st := r.Tools.ByTool[name]
			fmt.Fprintf(b, "| %s | %d | %d | %d | %d | %d |\n",
				name, st.Calls, st.Errors, st.Wasted, st.Duplicates, st.AvgResultBytes)
		}
	}
}

// ParseJudgeVerdict reads the model's reply into a verdict. Models routinely
// wrap JSON in prose or a code fence despite instructions, so the outermost
// JSON object is extracted rather than requiring a clean reply.
func ParseJudgeVerdict(reply string) (JudgeVerdict, error) {
	raw := extractJSONObject(reply)
	if raw == "" {
		return JudgeVerdict{}, fmt.Errorf("judge reply contains no JSON object: %q", truncate(oneLine(reply), 200))
	}

	var v JudgeVerdict
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return JudgeVerdict{}, fmt.Errorf("parse judge reply: %w", err)
	}
	if len(v.Scores) == 0 {
		return JudgeVerdict{}, fmt.Errorf("judge reply has no scores")
	}

	v.Overall = float64(clampScores(v.Scores)) / float64(len(v.Scores))
	v.Verdict = normalizeVerdict(v.Verdict, v.Scores, v.Overall)

	return v, nil
}

// clampScores pins every score into 1..5 in place and returns their total.
// Clamping rather than rejecting keeps the verdict usable: an out-of-range
// score is still a signal, and losing the whole grade over one bad number is
// worse.
func clampScores(scores []JudgeScore) int {
	total := 0
	for i, s := range scores {
		switch {
		case s.Score < 1:
			scores[i].Score = 1
		case s.Score > 5:
			scores[i].Score = 5
		}
		total += scores[i].Score
	}
	return total
}

// normalizeVerdict lowercases a stated pass/fail. When the model failed to
// state one, it is derived so the field is always meaningful: any score of 1
// is disqualifying, otherwise a mean of 3 passes.
func normalizeVerdict(stated string, scores []JudgeScore, overall float64) string {
	switch v := strings.ToLower(strings.TrimSpace(stated)); v {
	case "pass", "fail":
		return v
	}

	if overall < 3 {
		return "fail"
	}
	for _, s := range scores {
		if s.Score <= 1 {
			return "fail"
		}
	}
	return "pass"
}

// TrajectoryDigest condenses the loaded trajectories into the timeline the
// judge reads: one line per tool call, with truncated arguments and a marker on
// results that look like failures. Sessions are ordered as loaded (parent
// first), and each session's calls are capped so one runaway worker cannot
// crowd the others out of the prompt.
func TrajectoryDigest(loaded []*LoadedTrajectory, maxCallsPerSession int) string {
	if maxCallsPerSession <= 0 {
		maxCallsPerSession = 60
	}

	var b strings.Builder
	for _, lt := range loaded {
		if lt == nil || lt.Traj == nil {
			continue
		}
		digestSession(&b, lt, maxCallsPerSession)
	}
	return b.String()
}

// digestSession writes one session's block of the digest: a header line, then
// one line per tool call up to maxCalls, then a blank separator line. Hitting
// the cap replaces the remaining lines with a count of what was dropped.
func digestSession(b *strings.Builder, lt *LoadedTrajectory, maxCalls int) {
	t := lt.Traj
	fmt.Fprintf(b, "### session %s (agent %s)\n", shortID(t.SessionID), t.Agent.Name)

	results := resultsBySourceCall(lt)
	n := 0
	for _, step := range t.Steps {
		for _, tc := range step.ToolCalls {
			if n >= maxCalls {
				fmt.Fprintf(b, "... (%d more calls omitted)\n", countToolCalls(t)-n)
				b.WriteString("\n")
				return
			}
			n++
			fmt.Fprintf(b, "%d. %s(%s)", n, tc.FunctionName, truncate(canonicalArgs(tc.Arguments), 160))
			res, ok := results[tc.ToolCallID]
			writeCallOutcome(b, res, ok)
		}
	}
	if n == 0 {
		b.WriteString("(no tool calls)\n")
	}
	b.WriteString("\n")
}

// writeCallOutcome appends the result half of a tool-call line: the failure
// marker and message, the result size, or the absence of a result altogether.
func writeCallOutcome(b *strings.Builder, res atifResult, ok bool) {
	if !ok {
		b.WriteString(" -> (no result)\n")
		return
	}
	if looksLikeError(res.Content) {
		fmt.Fprintf(b, " -> ERROR: %s", truncate(oneLine(contentText(res.Content)), 160))
	} else {
		fmt.Fprintf(b, " -> %d bytes", contentBytes(res.Content))
	}
	if res.SubagentTrajectoryRef != "" {
		b.WriteString(" [spawned subagent]")
	}
	b.WriteString("\n")
}

// resultsBySourceCall indexes a trajectory's observation results by the tool
// call they answer.
func resultsBySourceCall(lt *LoadedTrajectory) map[string]atifResult {
	out := make(map[string]atifResult)
	for _, step := range lt.Traj.Steps {
		if step.Observation == nil {
			continue
		}
		for _, res := range step.Observation.Results {
			if res.SourceCallID == "" {
				continue
			}
			out[res.SourceCallID] = atifResult{
				Content:               res.Content,
				SubagentTrajectoryRef: res.SubagentTrajectoryRef,
			}
		}
	}
	return out
}

// atifResult is the slice of an observation result the digest needs, kept
// local so the digest does not depend on the atif package's shape.
type atifResult struct {
	Content               any
	SubagentTrajectoryRef string
}

// extractJSONObject returns the outermost {...} span of s, tolerating code
// fences and surrounding prose. Returns "" when there is no balanced object.
func extractJSONObject(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		switch {
		case escaped:
			escaped = false
		case c == '\\' && inString:
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
			// Braces inside strings do not nest.
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func contentText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(data)
	}
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.Join(strings.Fields(s), " ")
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
