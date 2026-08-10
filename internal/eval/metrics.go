// Package eval measures a real /run end-to-end: the ATIF trajectories it
// wrote, the subagent concurrency it reached, and how efficiently its tools
// were used. It is the metric layer behind the manually-run eval harness
// (make eval-run) — see eval.md. All computation is pure so the driving test
// (package tui, which has access to the unexported run handlers) stays thin.
package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/atif"
)

// --- Report structure ---

// RunReport is the complete machine-readable output of one eval run.
type RunReport struct {
	Metadata    ReportMetadata     `json:"metadata"`
	Outcome     RunOutcome         `json:"outcome"`
	Trajectory  TrajectoryMetrics  `json:"trajectory"`
	Concurrency ConcurrencyMetrics `json:"concurrency"`
	Tools       ToolsMetrics       `json:"tools"`
	Tokens      TokenMetrics       `json:"tokens"`
	// Judge is the LLM grader's advisory verdict, absent when no judge ran.
	Judge *JudgeVerdict `json:"judge,omitempty"`
}

// ReportMetadata describes what was run and when.
type ReportMetadata struct {
	Spec      string    `json:"spec"`
	Mode      string    `json:"mode"` // "single" | "parallel"
	Model     string    `json:"model"`
	Binary    string    `json:"binary"`
	GitHead   string    `json:"git_head"`
	Timestamp time.Time `json:"timestamp"`
	Duration  string    `json:"duration"`
	// BaseRef is the pinned starting point the run's worktree was created
	// from. Runs started from the same BaseRef are comparable; a run against
	// a moving HEAD is not comparable with anything.
	BaseRef string `json:"base_ref,omitempty"`
	// BaseCommit is BaseRef resolved to a commit, so a report stays meaningful
	// after the ref moves.
	BaseCommit string `json:"base_commit,omitempty"`
}

// RunOutcome captures how the run itself ended and whether the produced
// artifacts matched the golden copy.
type RunOutcome struct {
	FinalPhase string `json:"final_phase"` // "done", "failed", ...
	Retries    int    `json:"retries"`
	// Reason is the terminal failure message from the run flow ("Merge
	// failed…", "Verification failed…", …), empty when the run did not fail.
	Reason        string       `json:"reason,omitempty"`
	GateResults   []GateResult `json:"gate_results"`
	GoldenCheck   []GoldenFile `json:"golden_check"`
	GoldenPass    bool         `json:"golden_pass"`
	BaselineRef   string       `json:"baseline_ref,omitempty"`
	BaselineCheck []GoldenFile `json:"baseline_check,omitempty"`
	// BaselinePass is only meaningful when BaselineRef is non-empty.
	BaselinePass bool `json:"baseline_pass"`
}

// GateResult is the result of a single gate command.
type GateResult struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Passed  bool   `json:"passed"`
	Output  string `json:"output,omitempty"`
}

// GoldenFile is a per-file comparison between the produced artifacts and the
// expected golden copy.
type GoldenFile struct {
	Name  string `json:"name"`
	Match bool   `json:"match"`
	Diff  string `json:"diff,omitempty"`
	Error string `json:"error,omitempty"`
}

// --- Trajectory metrics ---

// LoadedTrajectory is a parsed ATIF trajectory with its source path and the
// session directory it lives in.
type LoadedTrajectory struct {
	Path       string
	SessionDir string
	SessionID  string
	Traj       *atif.Trajectory
}

// TrajectoryMetrics summarizes the trajectories written during the run,
// including nested subagent sessions and their nesting depth.
type TrajectoryMetrics struct {
	Sessions         []SessionSummary `json:"sessions"`
	TotalSteps       int              `json:"total_steps"`
	TotalToolCalls   int              `json:"total_tool_calls"`
	NestedAgentCalls int              `json:"nested_agent_calls"`
	MaxDepth         int              `json:"max_depth"`
}

// SessionSummary is a one-line summary of a single trajectory.
type SessionSummary struct {
	SessionID     string `json:"session_id"`
	AgentName     string `json:"agent_name"`
	Model         string `json:"model"`
	Steps         int    `json:"steps"`
	ToolCalls     int    `json:"tool_calls"`
	SubagentRefs  int    `json:"subagent_refs"`
	Depth         int    `json:"depth"`
	StartedAt     string `json:"started_at,omitempty"`
	Duration      string `json:"duration,omitempty"`
	ContinuedFrom string `json:"continued_from,omitempty"`
}

// --- Concurrency metrics ---

// ConcurrencySample is one poll of the orchestrator's live agent set.
type ConcurrencySample struct {
	Timestamp time.Time `json:"ts"`
	Running   int       `json:"running"`
	AgentIDs  []string  `json:"agent_ids,omitempty"`
}

// ConcurrencyMetrics summarizes the top-level orchestrator pool over time.
// Nested fan-out inside the coordinator process is not visible to the poller
// — it lives in other processes — so those numbers come from trajectories
// (see TrajectoryMetrics.NestedAgentCalls / MaxDepth).
type ConcurrencyMetrics struct {
	PoolBudget      int                 `json:"pool_budget"`
	WorkerBudget    int                 `json:"worker_budget"`
	MaxRunning      int                 `json:"max_running"`
	MeanRunning     float64             `json:"mean_running"`
	AgentsSeen      int                 `json:"agents_seen"`
	ParallelOverlap float64             `json:"parallel_overlap"`
	Samples         []ConcurrencySample `json:"samples"`
}

// --- Tools metrics ---

// ToolsMetrics aggregates tool usage across every trajectory in the run.
type ToolsMetrics struct {
	TotalCalls       int                  `json:"total_calls"`
	TotalResults     int                  `json:"total_results"`
	Wasted           int                  `json:"wasted"`
	Duplicates       int                  `json:"duplicates"`
	NestedAgentCalls int                  `json:"nested_agent_calls"`
	ByTool           map[string]ToolStats `json:"by_tool"`
}

// ToolStats is the per-tool efficiency summary.
//
//   - wasted: a tool_call with no matching observation result. The model asked
//     for a result that never arrived — either the call was interrupted or the
//     provider stream ended before the observation was recorded.
//   - duplicates: occurrences of an identical (function_name, arguments) pair
//     after the first. Repeating the exact same call suggests the model was
//     not paying attention to the result it already had.
//   - errors: results whose content looks like a failure (heuristic — see
//     errorMarkerRE). A "failed" bash call still produced a result, so it
//     counts as a result AND an error.
type ToolStats struct {
	Calls          int `json:"calls"`
	Results        int `json:"results"`
	Errors         int `json:"errors"`
	Wasted         int `json:"wasted"`
	Duplicates     int `json:"duplicates"`
	AvgResultBytes int `json:"avg_result_bytes"`
	AvgLatencyMs   int `json:"avg_latency_ms"`
}

// LoadTrajectories reads and parses every trajectory.atif.json under the
// sessions directory. Malformed files are skipped (their parse error is
// reported via the returned error if nothing at all parsed).
func LoadTrajectories(sessionsDir string) ([]*LoadedTrajectory, error) {
	pattern := filepath.Join(sessionsDir, "*", "trajectory.atif.json")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob trajectories: %w", err)
	}

	var loaded []*LoadedTrajectory
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var traj atif.Trajectory
		if err := json.Unmarshal(data, &traj); err != nil {
			continue // incomplete write or empty; not a usable trajectory
		}
		dir := filepath.Dir(p)
		loaded = append(loaded, &LoadedTrajectory{
			Path:       p,
			SessionDir: dir,
			SessionID:  filepath.Base(dir),
			Traj:       &traj,
		})
	}
	if len(loaded) == 0 && len(paths) > 0 {
		return nil, fmt.Errorf("no parseable trajectory found under %s (%d file(s) present)", sessionsDir, len(paths))
	}
	return loaded, nil
}

// ComputeTrajectoryMetrics builds the trajectory summary, computing nesting
// depth by following subagent_trajectory_ref links.
func ComputeTrajectoryMetrics(loaded []*LoadedTrajectory) TrajectoryMetrics {
	m := TrajectoryMetrics{}

	byID := make(map[string]*LoadedTrajectory, len(loaded))
	for _, lt := range loaded {
		byID[lt.SessionID] = lt
	}

	// childRefs[sessionID] → set of child session IDs referenced from its
	// observations. parentOf[child] → count of parents, for root detection.
	childRefs := make(map[string]map[string]bool)
	parentOf := make(map[string]int)
	for _, lt := range loaded {
		refs := subagentRefs(lt.Traj)
		if len(refs) > 0 {
			childRefs[lt.SessionID] = refs
			for child := range refs {
				parentOf[child]++
			}
		}
		m.NestedAgentCalls += len(refs)
	}

	// Roots are sessions nobody references. Depth is memoized DFS:
	// depth(child) = 1 + max(depth(parent)).
	depth := make(map[string]int, len(loaded))
	var depthOf func(id string, stack map[string]bool) int
	depthOf = func(id string, stack map[string]bool) int {
		if d, ok := depth[id]; ok {
			return d
		}
		if stack[id] { // cycle guard — should not happen, but never recurse forever
			return 0
		}
		stack[id] = true
		best := 0
		for parent := range parentsOf(id, loaded, childRefs) {
			if d := depthOf(parent, stack) + 1; d > best {
				best = d
			}
		}
		delete(stack, id)
		depth[id] = best
		return best
	}

	for _, lt := range loaded {
		t := lt.Traj
		toolCalls := countToolCalls(t)
		refs := len(subagentRefs(t))
		d := depthOf(lt.SessionID, map[string]bool{})

		started, duration := stepSpan(t)

		m.Sessions = append(m.Sessions, SessionSummary{
			SessionID:     lt.SessionID,
			AgentName:     t.Agent.Name,
			Model:         t.Agent.ModelName,
			Steps:         len(t.Steps),
			ToolCalls:     toolCalls,
			SubagentRefs:  refs,
			Depth:         d,
			StartedAt:     started,
			Duration:      duration,
			ContinuedFrom: t.ContinuedTrajectoryRef,
		})

		m.TotalSteps += len(t.Steps)
		m.TotalToolCalls += toolCalls
		if d > m.MaxDepth {
			m.MaxDepth = d
		}
	}

	// Deterministic ordering: depth first, then session id.
	sort.SliceStable(m.Sessions, func(i, j int) bool {
		if m.Sessions[i].Depth != m.Sessions[j].Depth {
			return m.Sessions[i].Depth < m.Sessions[j].Depth
		}
		return m.Sessions[i].SessionID < m.Sessions[j].SessionID
	})

	return m
}

// parentsOf returns the session IDs that directly reference id as a subagent.
func parentsOf(id string, loaded []*LoadedTrajectory, childRefs map[string]map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for parent, children := range childRefs {
		if children[id] {
			out[parent] = true
		}
	}
	return out
}

// subagentRefs returns the set of subagent trajectory refs (as session IDs)
// referenced from a trajectory's observations. The ref is an absolute path to
// the child's trajectory.atif.json, so the child session ID is its dir name.
func subagentRefs(t *atif.Trajectory) map[string]bool {
	out := make(map[string]bool)
	for i := range t.Steps {
		obs := t.Steps[i].Observation
		if obs == nil {
			continue
		}
		for _, r := range obs.Results {
			if r.SubagentTrajectoryRef == "" {
				continue
			}
			out[filepath.Base(filepath.Dir(r.SubagentTrajectoryRef))] = true
		}
	}
	return out
}

func countToolCalls(t *atif.Trajectory) int {
	n := 0
	for i := range t.Steps {
		n += len(t.Steps[i].ToolCalls)
	}
	return n
}

// stepSpan returns the first step timestamp and the span to the last step,
// as RFC3339 strings. Empty when no usable timestamps exist.
func stepSpan(t *atif.Trajectory) (started, duration string) {
	var first, last time.Time
	for i := range t.Steps {
		ts := t.Steps[i].Timestamp
		if ts == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339Nano, ts)
		if err != nil {
			continue
		}
		if first.IsZero() || parsed.Before(first) {
			first = parsed
		}
		if last.IsZero() || parsed.After(last) {
			last = parsed
		}
	}
	if first.IsZero() {
		return "", ""
	}
	return first.Format(time.RFC3339), last.Sub(first).Round(time.Second).String()
}

// ComputeConcurrencyMetrics summarizes the polled orchestrator state.
func ComputeConcurrencyMetrics(poolBudget int, samples []ConcurrencySample) ConcurrencyMetrics {
	m := ConcurrencyMetrics{
		PoolBudget:   poolBudget,
		WorkerBudget: childBudget(poolBudget),
		Samples:      samples,
	}

	seen := make(map[string]bool)
	var total int
	for _, s := range samples {
		if s.Running > m.MaxRunning {
			m.MaxRunning = s.Running
		}
		total += s.Running
		if s.Running > 1 {
			m.ParallelOverlap++
		}
		for _, id := range s.AgentIDs {
			seen[id] = true
		}
	}
	if n := len(samples); n > 0 {
		m.MeanRunning = float64(total) / float64(n)
		m.ParallelOverlap = m.ParallelOverlap / float64(n)
	}
	m.AgentsSeen = len(seen)
	return m
}

// childBudget mirrors subagent.childConcurrency: each spawned agent is its own
// pi process with its own pool, so the budget halves with depth (floor 1)
// rather than being inherited unchanged. Kept local to avoid exporting a
// production symbol purely for the eval harness.
func childBudget(parent int) int {
	if parent < 2 {
		return 1
	}
	return parent / 2
}

// --- Tools metrics ---

// callRecord is the intermediate bookkeeping for one tool call while pairing
// it with its observation.
type callRecord struct {
	stepTS    time.Time
	fn        string
	canonArgs string
	observed  bool
	obsStepTS time.Time
	obsBytes  int
	isError   bool
}

// ComputeToolsMetrics derives per-tool efficiency from the run's trajectories.
func ComputeToolsMetrics(loaded []*LoadedTrajectory) ToolsMetrics {
	m := ToolsMetrics{ByTool: make(map[string]ToolStats)}

	for _, lt := range loaded {
		records := pairCalls(lt.Traj)
		for id, rec := range records {
			st := m.ByTool[rec.fn]
			st.Calls++
			if rec.observed {
				st.Results++
				if rec.isError {
					st.Errors++
				}
				st.AvgResultBytes += rec.obsBytes
				if !rec.stepTS.IsZero() && !rec.obsStepTS.IsZero() {
					st.AvgLatencyMs += int(rec.obsStepTS.Sub(rec.stepTS).Milliseconds())
				}
			} else {
				st.Wasted++
			}
			m.ByTool[rec.fn] = st
			m.TotalCalls++
			if rec.observed {
				m.TotalResults++
			} else {
				m.Wasted++
			}
			if rec.fn == "subagent" {
				m.NestedAgentCalls++
			}
			_ = id
		}
	}

	// Per-tool averages and duplicate detection.
	for name := range m.ByTool {
		st := m.ByTool[name]
		if st.Results > 0 {
			st.AvgResultBytes /= st.Results
			st.AvgLatencyMs /= st.Results
		}
		st.Duplicates = duplicateCount(loaded, name)
		m.ByTool[name] = st
		m.Duplicates += st.Duplicates
	}

	// Deterministic tool ordering.
	sorted := make([]string, 0, len(m.ByTool))
	for name := range m.ByTool {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)
	ordered := make(map[string]ToolStats, len(sorted))
	for _, name := range sorted {
		ordered[name] = m.ByTool[name]
	}
	m.ByTool = ordered

	return m
}

// pairCalls walks a trajectory's steps in order, recording each tool call and
// then matching observations back to their calls by source_call_id. Latency is
// the wall time from the call's step to the observation's step.
func pairCalls(t *atif.Trajectory) map[string]*callRecord {
	records := make(map[string]*callRecord)
	for i := range t.Steps {
		step := &t.Steps[i]
		ts := parseStepTime(step.Timestamp)

		for j := range step.ToolCalls {
			call := &step.ToolCalls[j]
			id := call.ToolCallID
			if id == "" {
				id = fmt.Sprintf("s%d-c%d", step.StepID, j)
			}
			records[id] = &callRecord{
				stepTS:    ts,
				fn:        call.FunctionName,
				canonArgs: canonicalArgs(call.Arguments),
			}
		}

		if step.Observation == nil {
			continue
		}
		for j := range step.Observation.Results {
			r := &step.Observation.Results[j]
			rec, ok := records[r.SourceCallID]
			if !ok {
				continue
			}
			rec.observed = true
			rec.obsStepTS = ts
			rec.obsBytes = contentBytes(r.Content)
			rec.isError = looksLikeError(r.Content)
		}
	}
	return records
}

// duplicateCount counts repeated identical (function_name, arguments) pairs
// for one tool across all trajectories: occurrences after the first.
func duplicateCount(loaded []*LoadedTrajectory, tool string) int {
	counts := make(map[string]int)
	for _, lt := range loaded {
		for i := range lt.Traj.Steps {
			for j := range lt.Traj.Steps[i].ToolCalls {
				call := &lt.Traj.Steps[i].ToolCalls[j]
				if call.FunctionName != tool {
					continue
				}
				counts[canonicalArgs(call.Arguments)]++
			}
		}
	}
	dups := 0
	for _, n := range counts {
		if n > 1 {
			dups += n - 1
		}
	}
	return dups
}

func parseStepTime(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

// canonicalArgs renders tool arguments as a stable string for duplicate
// detection (map iteration order is random).
func canonicalArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	b, err := json.Marshal(args)
	if err != nil {
		return fmt.Sprintf("%v", args)
	}
	return string(b)
}

// contentBytes returns the byte length of an observation result, handling both
// string and structured content.
func contentBytes(content any) int {
	switch v := content.(type) {
	case string:
		return len(v)
	case nil:
		return 0
	default:
		if b, err := json.Marshal(v); err == nil {
			return len(b)
		}
		return 0
	}
}

// looksLikeError applies a light heuristic to a tool result: content that
// reads as a failure. It is intentionally conservative — a successful tool can
// legitimately contain the word "error" in its output, but the most common
// failure signatures are distinctive enough to be worth counting.
func looksLikeError(content any) bool {
	s, ok := content.(string)
	if !ok {
		return false
	}
	lower := strings.ToLower(s)
	for _, marker := range []string{
		"error:", "exit status", "panic:", "failed", "timed out", "not found",
		"permission denied", "command not found", "fatal:",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// --- Token metrics ---

// TokenMetrics aggregates provider-reported token usage across every session
// in the run. Usage is read from each session's events.jsonl because the ATIF
// trajectory does not carry it.
type TokenMetrics struct {
	PromptTokens     int                 `json:"prompt_tokens"`
	CompletionTokens int                 `json:"completion_tokens"`
	CachedTokens     int                 `json:"cached_tokens"`
	TotalTokens      int                 `json:"total_tokens"`
	CostUSD          float64             `json:"cost_usd,omitempty"`
	Sessions         []SessionTokenUsage `json:"sessions,omitempty"`
}

// SessionTokenUsage is the token usage attributed to one session.
type SessionTokenUsage struct {
	SessionID        string `json:"session_id"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	CachedTokens     int    `json:"cached_tokens"`
}

// usageEvent is the subset of a persisted session event we need: the provider
// usage block. Partial events are never persisted (FileService.AppendEvent
// drops them), so every event with a non-nil UsageMetadata is a final response
// and summing them does not double-count a streamed turn.
type usageEvent struct {
	UsageMetadata *struct {
		PromptTokenCount        int `json:"promptTokenCount"`
		CandidatesTokenCount    int `json:"candidatesTokenCount"`
		CachedContentTokenCount int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
}

// ComputeTokenMetrics sums provider-reported token usage across every session
// in the run, attributing each session's usage to its trajectory.
func ComputeTokenMetrics(loaded []*LoadedTrajectory) TokenMetrics {
	m := TokenMetrics{}
	for _, lt := range loaded {
		u := sessionTokenUsage(lt.SessionDir)
		m.PromptTokens += u.PromptTokens
		m.CompletionTokens += u.CompletionTokens
		m.CachedTokens += u.CachedTokens
		if u.PromptTokens > 0 || u.CompletionTokens > 0 || u.CachedTokens > 0 {
			u.SessionID = lt.SessionID
			m.Sessions = append(m.Sessions, u)
		}
	}
	m.TotalTokens = m.PromptTokens + m.CompletionTokens
	return m
}

// sessionTokenUsage reads one session's events.jsonl and sums its usage blocks.
func sessionTokenUsage(sessionDir string) SessionTokenUsage {
	var u SessionTokenUsage
	f, err := os.Open(filepath.Join(sessionDir, "events.jsonl"))
	if err != nil {
		return u
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)
	for scanner.Scan() {
		var ev usageEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		if ev.UsageMetadata == nil {
			continue
		}
		u.PromptTokens += ev.UsageMetadata.PromptTokenCount
		u.CompletionTokens += ev.UsageMetadata.CandidatesTokenCount
		u.CachedTokens += ev.UsageMetadata.CachedContentTokenCount
	}
	return u
}

// DiffGolden compares each named file between the produced artifacts directory
// and the golden copy. Returns the per-file match report and the overall pass.
// A produced file that is missing counts as a mismatch.
func DiffGolden(producedDir, goldenDir string, files []string) ([]GoldenFile, bool) {
	all := true
	checks := make([]GoldenFile, 0, len(files))
	for _, name := range files {
		g := GoldenFile{Name: name, Match: true}
		want, err := os.ReadFile(filepath.Join(goldenDir, name))
		if err != nil {
			g.Match = false
			g.Error = fmt.Sprintf("golden copy missing: %v", err)
			all = false
			checks = append(checks, g)
			continue
		}
		got, err := os.ReadFile(filepath.Join(producedDir, name))
		if err != nil {
			g.Match = false
			g.Error = fmt.Sprintf("produced file missing: %v", err)
			all = false
			checks = append(checks, g)
			continue
		}
		if string(got) != string(want) {
			g.Match = false
			g.Diff = firstDiff(want, got)
			all = false
		}
		checks = append(checks, g)
	}
	return checks, all
}

// firstDiff returns a short line-oriented snippet around the first difference
// between two byte slices, plus the differing line number.
func firstDiff(want, got []byte) string {
	wantLines := strings.Split(string(want), "\n")
	gotLines := strings.Split(string(got), "\n")
	n := len(wantLines)
	if len(gotLines) < n {
		n = len(gotLines)
	}
	idx := -1
	for i := 0; i < n; i++ {
		if wantLines[i] != gotLines[i] {
			idx = i
			break
		}
	}
	if idx == -1 {
		idx = n // one is a prefix of the other; differ past the shared prefix
	}

	var b strings.Builder
	fmt.Fprintf(&b, "first difference at line %d\n", idx+1)
	snippet := func(lines []string, marker string, around int) {
		lo := max(0, idx-around)
		hi := min(len(lines), idx+around+1)
		for i := lo; i < hi; i++ {
			line := lines[i]
			if len(line) > 80 {
				line = line[:80] + "…"
			}
			fmt.Fprintf(&b, "%s %4d| %s\n", marker, i+1, line)
		}
	}
	snippet(wantLines, "-", 2)
	snippet(gotLines, "+", 2)
	return b.String()
}
