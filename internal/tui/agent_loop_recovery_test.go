package tui

import (
	"errors"
	"strings"
	"testing"
)

func TestStuckErrIsRecoverable(t *testing.T) {
	err := stuckErr(true, "model repeated a 106-character phrase 12 times")

	var stuck *stuckError
	if !errors.As(err, &stuck) {
		t.Fatalf("stuckErr returned %T, want *stuckError so the run loop can recover", err)
	}
	if stuck.Detail() != "model repeated a 106-character phrase 12 times" {
		t.Fatalf("Detail() = %q, want the detector's reason verbatim", stuck.Detail())
	}
	// The rendered message is what the user sees; keep the existing wording so
	// log greps and the e2e assertions on "aborted" keep working.
	if !strings.Contains(err.Error(), "agent loop aborted: ") {
		t.Fatalf("Error() = %q, want it to keep the 'agent loop aborted' prefix", err.Error())
	}
}

func TestStuckErrNilWhenNotStuck(t *testing.T) {
	if err := stuckErr(false, "ignored"); err != nil {
		t.Fatalf("stuckErr(false) = %v, want nil", err)
	}
}

// TestRecoverStuckPromptNamesTheFailure pins the property that makes recovery
// work at all: the model is told what specifically tripped the guard. A generic
// "try again" tends to reproduce the same output, because nothing in the
// conversation tells the model what went wrong.
func TestRecoverStuckPromptNamesTheFailure(t *testing.T) {
	detail := "identical tool call \"read\" repeated 10 times"
	got := recoverStuckPrompt(detail)

	if !strings.Contains(got, detail) {
		t.Fatalf("recovery prompt does not contain the detector's reason %q:\n%s", detail, got)
	}
	for _, want := range []string{
		"stopped automatically",
		"Do not",
		"Change approach",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("recovery prompt missing %q:\n%s", want, got)
		}
	}
}

// TestMaxStuckRecoveriesIsBounded guards the cost side of this feature. An
// unbounded retry turns a runaway loop into a runaway bill, which is the exact
// thing the stuck detector exists to prevent.
func TestMaxStuckRecoveriesIsBounded(t *testing.T) {
	if maxStuckRecoveries < 1 {
		t.Fatalf("maxStuckRecoveries = %d, want >= 1 or the feature does nothing", maxStuckRecoveries)
	}
	if maxStuckRecoveries > 3 {
		t.Fatalf("maxStuckRecoveries = %d: each attempt costs a full turn, keep the budget small", maxStuckRecoveries)
	}
}
