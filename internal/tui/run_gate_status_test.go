package tui

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunGatesClassifiesPassFailHang(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    GateStatus
	}{
		{"pass", "true", GatePass},
		{"fail", "exit 3", GateFail},
		{"hang", "sleep 30", GateHang},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := runGatesTimeout(t.Context(), t.TempDir(),
				[]Gate{{Name: tt.name, Command: tt.command}}, 150*time.Millisecond)

			if len(msg.results) != 1 {
				t.Fatalf("got %d results, want 1", len(msg.results))
			}
			got := msg.results[0]
			if got.Status != tt.want {
				t.Errorf("Status = %q, want %q", got.Status, tt.want)
			}
			if wantPassed := tt.want == GatePass; got.Passed != wantPassed {
				t.Errorf("Passed = %v, want %v", got.Passed, wantPassed)
			}
			if msg.hung != (tt.want == GateHang) {
				t.Errorf("msg.hung = %v, want %v", msg.hung, tt.want == GateHang)
			}
			if msg.passed != (tt.want == GatePass) {
				t.Errorf("msg.passed = %v, want %v", msg.passed, tt.want == GatePass)
			}
		})
	}
}

// A hang must not be reported as a pass. This is the specific laundering the
// three-valued status exists to prevent.
func TestRunGatesHangIsNotAPass(t *testing.T) {
	msg := runGatesTimeout(t.Context(), t.TempDir(),
		[]Gate{{Name: "test", Command: "sleep 30"}}, 100*time.Millisecond)

	if msg.passed {
		t.Fatal("a hung gate was reported as passed")
	}
	if !strings.Contains(msg.results[0].Output, "did not return within") {
		t.Errorf("hang output does not explain the timeout: %q", msg.results[0].Output)
	}
}

// A caller-canceled gate is a teardown, not a misbehaving command: it must be
// classified FAIL so the hang path is reserved for commands that really hang.
func TestRunGatesCallerCancelIsFailNotHang(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		time.Sleep(80 * time.Millisecond)
		cancel()
	}()
	msg := runGatesTimeout(ctx, t.TempDir(),
		[]Gate{{Name: "test", Command: "sleep 30"}}, 10*time.Second)

	if msg.results[0].Status != GateFail {
		t.Errorf("Status = %q, want %q for a caller-canceled gate", msg.results[0].Status, GateFail)
	}
	if msg.hung {
		t.Error("caller cancellation was misreported as a hang")
	}
}

func TestRunGatesStopsAtFirstNonPass(t *testing.T) {
	msg := runGatesTimeout(t.Context(), t.TempDir(), []Gate{
		{Name: "build", Command: "true"},
		{Name: "test", Command: "exit 1"},
		{Name: "vet", Command: "true"},
	}, time.Second)

	if len(msg.results) != 2 {
		t.Fatalf("got %d results, want 2 (should stop at the failing gate)", len(msg.results))
	}
}

// status() must keep working for GateResults built before Status existed.
func TestGateResultStatusFallsBackToPassed(t *testing.T) {
	if got := (GateResult{Passed: true}).status(); got != GatePass {
		t.Errorf("status() = %q, want %q", got, GatePass)
	}
	if got := (GateResult{Passed: false}).status(); got != GateFail {
		t.Errorf("status() = %q, want %q", got, GateFail)
	}
	if got := (GateResult{Passed: false, Status: GateHang}).status(); got != GateHang {
		t.Errorf("status() = %q, want %q", got, GateHang)
	}
}

func TestWriteRunSummaryGatesReportsHangDistinctly(t *testing.T) {
	var b strings.Builder
	writeRunSummaryGates(&b, &runState{
		gateResults: []GateResult{
			{Name: "build", Command: "make build", Passed: true, Status: GatePass},
			{Name: "test", Command: "make test", Status: GateHang, Elapsed: 10 * time.Minute},
		},
	})
	out := b.String()
	if !strings.Contains(out, "HANG") {
		t.Errorf("summary does not mark the gate HANG:\n%s", out)
	}
	if strings.Contains(out, "Some gates **failed**") {
		t.Errorf("a hang was described as a failure:\n%s", out)
	}
	if !strings.Contains(out, "never returned") {
		t.Errorf("summary does not explain the hang:\n%s", out)
	}
}
