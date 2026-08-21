package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// fixedRunStart is a stable instant for the Started metadata row. The Duration
// row derived from it is wall-clock dependent and gets normalized out below.
var fixedRunStart = time.Date(2026, 3, 14, 9, 26, 53, 0, time.UTC)

// durationRow matches the one row of the report that cannot be pinned:
// Duration is time.Since(startTime), so it grows between runs.
var durationRow = regexp.MustCompile(`(?m)^\| Duration \| .*$`)

// normalizeRunSummary replaces the wall-clock Duration value with a fixed
// marker, leaving the row itself in place so its presence is still pinned.
func normalizeRunSummary(report string) string {
	return durationRow.ReplaceAllString(report, "| Duration | <elapsed> |")
}

type runSummaryCase struct {
	name    string
	state   func() *runState
	outcome string
}

func runSummaryCases() []runSummaryCase {
	twoGates := []Gate{
		{Name: "build", Command: "go build ./..."},
		{Name: "test", Command: "go test ./..."},
	}
	base := func() *runState {
		return &runState{
			specName:   "rate-limiter",
			agentID:    "task-42",
			phase:      "done",
			retries:    1,
			maxRetries: 3,
			startTime:  fixedRunStart,
		}
	}
	withChecklist := func(rs *runState) *runState {
		rs.checklist = []ChecklistStep{
			{Title: "parse the config", Done: true},
			{Title: "wire the limiter", Done: true},
			{Title: "add metrics", Done: false},
		}
		return rs
	}

	return []runSummaryCase{
		{
			// Nothing optional present: no gates, no checklist, no start time.
			name:    "minimal",
			outcome: "completed",
			state: func() *runState {
				return &runState{specName: "bare", agentID: "task-0", maxRetries: 3}
			},
		},
		{
			name:    "gates defined but never executed",
			outcome: "agent_failed",
			state: func() *runState {
				rs := base()
				rs.gates = twoGates
				return rs
			},
		},
		{
			name:    "all gates passed",
			outcome: "completed",
			state: func() *runState {
				rs := withChecklist(base())
				rs.checklist[2].Done = true
				rs.gates = twoGates
				rs.gateResults = []GateResult{
					{Name: "build", Command: "go build ./...", Passed: true},
					{Name: "test", Command: "go test ./...", Passed: true},
				}
				return rs
			},
		},
		{
			name:    "a gate failed with output",
			outcome: "gate_failed",
			state: func() *runState {
				rs := withChecklist(base())
				rs.gates = twoGates
				rs.gateResults = []GateResult{
					{Name: "build", Command: "go build ./...", Passed: true},
					{Name: "test", Command: "go test ./...", Passed: false,
						Output: "  \n--- FAIL: TestThing (0.01s)\n    thing_test.go:42: want 1, got 2\nFAIL\n  "},
				}
				return rs
			},
		},
		{
			// Output past 1000 characters is truncated with a marker.
			name:    "gate output is truncated past the cap",
			outcome: "gate_failed",
			state: func() *runState {
				rs := base()
				rs.gates = twoGates
				long := ""
				for range 200 {
					long += "failing line\n"
				}
				rs.gateResults = []GateResult{
					{Name: "test", Command: "go test ./...", Passed: false, Output: long},
				}
				return rs
			},
		},
		{
			// A failed gate with no captured output must not emit an empty
			// fence.
			name:    "failed gate with no output",
			outcome: "gate_failed",
			state: func() *runState {
				rs := base()
				rs.gateResults = []GateResult{{Name: "lint", Command: "golangci-lint run", Passed: false}}
				return rs
			},
		},
		{
			name:    "unfinished slices are listed",
			outcome: "verify_failed",
			state:   func() *runState { return withChecklist(base()) },
		},
		{
			name:    "merge failed",
			outcome: "merge_failed",
			state:   func() *runState { return withChecklist(base()) },
		},
		{
			// The default arm of the outcome switch — an outcome the report
			// has no prose for still has to say something.
			name:    "unrecognized outcome falls through to the default",
			outcome: "interrupted_by_user",
			state:   func() *runState { return base() },
		},
	}
}

// TestBuildRunSummaryReportGolden pins the whole rendered SUMMARY.md. This is
// the characterization net for splitting the report into sections: it catches
// a dropped blank line or a reordered section, which a Contains-style
// assertion cannot.
func TestBuildRunSummaryReportGolden(t *testing.T) {
	t.Parallel()

	for _, tc := range runSummaryCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeRunSummary(buildRunSummaryReport(tc.state(), tc.outcome))
			assertRunGolden(t, "run_summary", tc.name, got)
		})
	}
}

// The Duration row is normalized out of the goldens, so its format is pinned
// here instead: it must be present whenever a start time was recorded, and
// absent when one was not.
func TestBuildRunSummaryReportDurationRow(t *testing.T) {
	t.Parallel()

	withStart := buildRunSummaryReport(&runState{specName: "s", startTime: fixedRunStart}, "completed")
	if !durationRow.MatchString(withStart) {
		t.Errorf("expected a Duration row when startTime is set, got:\n%s", withStart)
	}
	// The goldens normalize the elapsed value away, so its granularity is
	// pinned here instead: the duration is truncated to whole seconds, and a
	// sub-second unit in the output means that truncation was lost.
	value := durationRow.FindString(withStart)
	for _, subSecond := range []string{".", "ms", "µs", "ns"} {
		if strings.Contains(strings.TrimSuffix(value, " |"), subSecond) {
			t.Errorf("Duration row %q carries sub-second precision (%q); it must be truncated to seconds",
				value, subSecond)
		}
	}
	if !regexp.MustCompile(`\| Started \| 2026-03-14T09:26:53Z \|`).MatchString(withStart) {
		t.Errorf("expected the RFC3339 Started row, got:\n%s", withStart)
	}

	noStart := buildRunSummaryReport(&runState{specName: "s"}, "completed")
	if durationRow.MatchString(noStart) {
		t.Errorf("expected no Duration row without a startTime, got:\n%s", noStart)
	}
}
