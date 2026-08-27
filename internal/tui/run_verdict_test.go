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
