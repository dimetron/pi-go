package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/sop"
	sopexec "github.com/dimetron/pi-go/internal/sop/exec"
)

// TestPRTargetAccepted pins which forms name a pull request. gh accepts a URL,
// a #123 and a bare number; anything else is a typo worth rejecting before a
// run starts rather than after gh fails.
func TestPRTargetAccepted(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"https://github.com/dimetron/pi-go/pull/253", true},
		{"#253", true},
		{"253", true},
		{"", false},
		{"main", false},
		{"https://github.com/dimetron/pi-go/issues/253", false},
		{"https://example.com/dimetron/pi-go/pull/253", false},
		{"253; rm -rf /", false},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := prNumber.MatchString(tt.in); got != tt.want {
				t.Errorf("prNumber.MatchString(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// TestPRAutofixStagePromptEmbedsInputs verifies the fix agent is handed the
// evidence rather than told where to find it.
func TestPRAutofixStagePromptEmbedsInputs(t *testing.T) {
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "failures.log"), []byte("undefined: Foo"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	stage := sop.Stage{
		ID:          "fix",
		Description: "Fix the failing checks in the working tree",
		Agent:       "worker",
		Inputs:      []string{"failures.log", "missing.json"},
	}
	got := prAutofixStagePrompt(stage, runDir)

	if !strings.Contains(got, stage.Description) {
		t.Error("prompt should carry the stage's own description")
	}
	if !strings.Contains(got, "undefined: Foo") {
		t.Error("prompt should embed the input's content, not just its name")
	}
	if !strings.Contains(got, "Do not commit, push") {
		t.Error("prompt should tell the agent that later stages own the commit")
	}
	// A declared input that no stage wrote is skipped rather than fatal: the
	// prompt is still useful with the evidence that does exist.
	if strings.Contains(got, "--- missing.json ---") {
		t.Error("a missing input should be skipped, not announced")
	}
}

// TestTruncateForPromptKeepsTail verifies an oversized log keeps its end,
// where a failing job says what actually went wrong.
func TestTruncateForPromptKeepsTail(t *testing.T) {
	body := strings.Repeat("noise\n", 20000) + "FINAL: the real error"
	got := truncateForPrompt(body)

	if len(got) > 25*1024 {
		t.Errorf("truncated length = %d, want it bounded", len(got))
	}
	if !strings.Contains(got, "FINAL: the real error") {
		t.Error("truncation dropped the tail, which is the part that matters")
	}
	if !strings.Contains(got, "truncated") {
		t.Error("truncation should say it happened")
	}
}

// TestTruncateForPromptLeavesShortInputAlone verifies a log that fits is passed
// through untouched.
func TestTruncateForPromptLeavesShortInputAlone(t *testing.T) {
	const short = "one line"
	if got := truncateForPrompt(short); got != short {
		t.Errorf("truncateForPrompt(%q) = %q, want it unchanged", short, got)
	}
}

// TestPRAutofixStageLineTrims verifies a long stage output is trimmed for the
// transcript. The whole log belongs in the artifact the fix agent reads; the
// chat gets a readable excerpt.
func TestPRAutofixStageLineTrims(t *testing.T) {
	out := strings.Repeat("line\n", 40)
	got := prAutofixStageLine("diagnose", out)

	if !strings.HasPrefix(got, "[diagnose] ") {
		t.Errorf("line = %q, want it to name its stage", got)
	}
	if n := strings.Count(got, "\n") + 1; n > 14 {
		t.Errorf("rendered %d lines, want a trimmed excerpt", n)
	}
	if !strings.Contains(got, "more lines") {
		t.Error("a trimmed excerpt should say how much it dropped")
	}
}

// TestPRAutofixStageLineKeepsShortOutput verifies output that already fits is
// shown whole.
func TestPRAutofixStageLineKeepsShortOutput(t *testing.T) {
	got := prAutofixStageLine("triage", "2 checks failing\n")
	if got != "[triage] 2 checks failing" {
		t.Errorf("line = %q, want the output kept whole", got)
	}
}

// TestPRAutofixSOPDrawsInSidebar verifies the mode's graph is the compiled SOP
// — the diagram and the executed workflow are the same definition, so they
// cannot drift.
func TestPRAutofixSOPDrawsInSidebar(t *testing.T) {
	def, err := sop.LoadEmbeddedDefinition("pr-autofix")
	if err != nil {
		t.Fatalf("LoadEmbeddedDefinition: %v", err)
	}
	compiled, err := sop.Compile(def, sop.DescribeFactory{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	want := []string{"resolve", "watch", "triage", "diagnose", "fix", "gates", "commit", "push", "summary"}
	for _, id := range want {
		if !slices.Contains(compiled.Order, id) {
			t.Errorf("compiled order %v is missing stage %q", compiled.Order, id)
		}
	}
}

// TestPRAutofixStageStartExtractsCycle verifies the lifecycle event parser
// recovers both the stage id and the cycle number, so the narration can say
// "cycle 2" when the graph loops back.
func TestPRAutofixStageStartExtractsCycle(t *testing.T) {
	tests := []struct {
		name      string
		branch    string
		author    string
		wantStage string
		wantCycle int
		wantOK    bool
	}{
		{"first activation", "push", sopexec.LifecycleAuthor, "push", 1, true},
		{"second cycle", "watch#2", sopexec.LifecycleAuthor, "watch", 2, true},
		{"third cycle", "watch#3", sopexec.LifecycleAuthor, "watch", 3, true},
		{"not a lifecycle event", "push", "model", "", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := &session.Event{Author: tt.author, Branch: tt.branch}
			stage, cycle, ok := prAutofixStageStart(ev)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if stage != tt.wantStage {
				t.Errorf("stage = %q, want %q", stage, tt.wantStage)
			}
			if cycle != tt.wantCycle {
				t.Errorf("cycle = %d, want %d", cycle, tt.wantCycle)
			}
		})
	}
}

// TestPRAutofixStartLineNarratesStage verifies the start line names the stage,
// carries its description, and includes the cycle number on loops > 1.
func TestPRAutofixStartLineNarratesStage(t *testing.T) {
	if got := prAutofixStartLine("push", 1); !strings.Contains(got, "push") {
		t.Errorf("first cycle: %q should name the stage", got)
	}
	if got := prAutofixStartLine("push", 1); !strings.Contains(got, "Pushing to the PR branch") {
		t.Errorf("first cycle: %q should carry the stage's description", got)
	}
	if got := prAutofixStartLine("watch", 2); !strings.Contains(got, "cycle 2") {
		t.Errorf("second cycle: %q should say which cycle it is", got)
	}
	// An unknown stage falls back to its id rather than empty text.
	if got := prAutofixStartLine("unknown", 1); !strings.Contains(got, "unknown") {
		t.Errorf("unknown stage: %q should fall back to its id", got)
	}
}
