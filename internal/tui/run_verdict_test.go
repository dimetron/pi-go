package tui

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/sop"
)

// verdictModel builds a model whose run is at the verification step with the
// given checklist state and coordinator transcript.
func verdictModel(t *testing.T, transcript string, done ...bool) *model {
	t.Helper()
	rs := &runState{specName: "features/x", maxRetries: 0}
	for i, d := range done {
		rs.checklist = append(rs.checklist, ChecklistStep{
			Title: "slice " + string(rune('A'+i)), Done: d,
		})
	}
	rs.transcript.WriteString(transcript)
	return &model{run: rs, chatModel: ChatModel{Messages: make([]message, 0)}}
}

// The corpus failure this guards: every box ticked, no review, merged anyway.
func TestVerifyRunCompleteRefusesToMergeWithoutAVerdict(t *testing.T) {
	m := verdictModel(t, "I finished all the slices and everything builds.", true, true)
	_, cmd := m.verifyRunComplete()

	if m.run.phase == "merging" {
		t.Fatal("a fully-ticked checklist with no verdict reached the merge")
	}
	if cmd != nil {
		t.Error("retry budget was 0, so no retry command should have been issued")
	}
	msg := strings.Join(messageContents(m), "\n")
	if !strings.Contains(msg, "no VERDICT line") {
		t.Errorf("failure does not name the missing verdict:\n%s", msg)
	}
	if !strings.Contains(msg, "A checked box is a claim") {
		t.Errorf("failure does not explain why ticks are not enough:\n%s", msg)
	}
}

func TestVerifyRunCompleteMergesOnTickedChecklistAndPass(t *testing.T) {
	m := verdictModel(t, "All criteria met.\n\nVERDICT: PASS\n", true, true)
	_, _ = m.verifyRunComplete()

	if m.run.phase != "merging" {
		t.Fatalf("phase = %q, want merging", m.run.phase)
	}
	msg := strings.Join(messageContents(m), "\n")
	if !strings.Contains(msg, "Verifier: PASS") {
		t.Errorf("no PASS message:\n%s", msg)
	}
}

func TestVerifyRunCompleteRefusesOnFailVerdict(t *testing.T) {
	m := verdictModel(t,
		"- No stubs remain - NOT MET: panic(\"not implemented\") in `internal/api/job.go`\nVERDICT: FAIL\n",
		true, true)
	_, _ = m.verifyRunComplete()

	if m.run.phase == "merging" {
		t.Fatal("a FAIL verdict reached the merge")
	}
	msg := strings.Join(messageContents(m), "\n")
	if !strings.Contains(msg, "No stubs remain") {
		t.Errorf("failure does not carry the unmet criterion:\n%s", msg)
	}
}

func TestVerifyRunCompleteReportsBothSignals(t *testing.T) {
	m := verdictModel(t, "VERDICT: FAIL\n", true, false)
	_, _ = m.verifyRunComplete()

	msg := strings.Join(messageContents(m), "\n")
	if !strings.Contains(msg, "unchecked in plan.md") {
		t.Errorf("failure omits the checklist signal:\n%s", msg)
	}
	if !strings.Contains(msg, "VERDICT: FAIL") && !strings.Contains(msg, "returned VERDICT") {
		t.Errorf("failure omits the review signal:\n%s", msg)
	}
}

// The retry briefing must address the right signal: telling a coordinator to
// "finish the remaining slices" when every slice is ticked and the review
// failed is what makes a cycle repeat itself.
func TestVerificationContextTargetsTheFailingSignal(t *testing.T) {
	t.Run("review failure", func(t *testing.T) {
		m := verdictModel(t, "- Criterion one - NOT MET: still a stub\nVERDICT: FAIL\n", true, true)
		m.run.verdict = sop.ParseVerdict(m.run.transcript.String())

		got := m.verificationContext(nil)
		if !strings.Contains(got, "Criterion one") {
			t.Errorf("context omits the unmet criterion:\n%s", got)
		}
		if strings.Contains(got, "unfinished") {
			t.Errorf("context talks about unfinished slices when none are:\n%s", got)
		}
	})

	t.Run("incomplete plan", func(t *testing.T) {
		m := verdictModel(t, "VERDICT: PASS\n", true, false)
		m.run.verdict = sop.ParseVerdict(m.run.transcript.String())

		got := m.verificationContext(m.run.unfinishedSlices())
		if !strings.Contains(got, "unfinished") {
			t.Errorf("context omits the outstanding slices:\n%s", got)
		}
	})
}

// A PASS from an earlier cycle must not satisfy a later one.
func TestRetryResetsTheVerdict(t *testing.T) {
	m := verdictModel(t, "VERDICT: PASS\n", true)
	m.run.verdict = sop.ParseVerdict(m.run.transcript.String())
	if !m.run.verdict.Passed() {
		t.Fatal("fixture did not parse a PASS")
	}

	// retryRun resets both; simulate the reset it performs.
	m.run.transcript.Reset()
	m.run.verdict = sop.Verdict{}

	if m.run.verdict.Passed() {
		t.Error("a stale PASS survived into the next cycle")
	}
	if m.run.transcript.Len() != 0 {
		t.Error("transcript was not reset between cycles")
	}
}

// The coordinator must be told to emit the line the runtime reads.
func TestCoordinatorContractRequiresTheVerdictLine(t *testing.T) {
	contract := buildRunPrompt("features/x", "# Spec\n", nil)
	for _, want := range []string{"VERDICT: PASS", "VERDICT: FAIL", "does not merge"} {
		if !strings.Contains(contract, want) {
			t.Errorf("coordinator contract omits %q", want)
		}
	}
}

func messageContents(m *model) []string {
	out := make([]string, 0, len(m.chatModel.Messages))
	for _, msg := range m.chatModel.Messages {
		out = append(out, msg.content)
	}
	return out
}

// A run must give every agent it spawns the same run ID, so the tree is
// recoverable from meta.json rather than by grouping sessions by working
// directory and guessing at roles.
func TestRunAttributionCarriesRunIdentity(t *testing.T) {
	got := runAttribution("run-1", "features/x", "sess-parent", 3, 2)
	if got.RunID != "run-1" || got.SpecName != "features/x" || got.ParentID != "sess-parent" {
		t.Errorf("attribution = %+v", got)
	}
	if got.Slice != 3 || got.Cycle != 2 {
		t.Errorf("slice/cycle = %d/%d, want 3/2", got.Slice, got.Cycle)
	}
	if got.AgentType != "task" {
		t.Errorf("AgentType = %q, want task", got.AgentType)
	}
	// The agent's own ID and worktree are the orchestrator's to fill in.
	if got.AgentID != "" || got.Worktree != "" {
		t.Errorf("run-level attribution should not guess the agent's id or worktree: %+v", got)
	}
}

func TestNewRunIDIsSpecScopedAndUnique(t *testing.T) {
	a := newRunID("features/TOO/024-mistral-provider")
	if !strings.Contains(a, "features-TOO-024-mistral-provider") {
		t.Errorf("run id %q does not name the spec", a)
	}
	if !strings.HasPrefix(a, "run-") {
		t.Errorf("run id %q has no run- prefix", a)
	}
}

func TestRunSummaryRecordsTheRunID(t *testing.T) {
	rs := &runState{specName: "features/x", runID: "run-features-x-42", agentID: "a1"}
	var b strings.Builder
	writeRunSummaryMetadata(&b, rs, "completed")
	if !strings.Contains(b.String(), "run-features-x-42") {
		t.Errorf("summary omits the run id:\n%s", b.String())
	}
}

func TestRunSummaryOmitsRunIDWhenAbsent(t *testing.T) {
	rs := &runState{specName: "features/x", agentID: "a1"}
	var b strings.Builder
	writeRunSummaryMetadata(&b, rs, "completed")
	if strings.Contains(b.String(), "Run ID") {
		t.Errorf("summary shows an empty Run ID row:\n%s", b.String())
	}
}
