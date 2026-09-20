package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// reportGateHang is the terminal path for a gate that never returned. It had no
// test, and it is the one place a hang is distinguished from a failure: the
// message must say not-returned rather than failed, because "re-run it" is the
// wrong advice for a hang (pi-go's own suite hangs under the sandbox, so the
// usual resolution is to run the gate by hand outside it).
func TestReportGateHang(t *testing.T) {
	newModel := func(t *testing.T) *model {
		t.Helper()
		workDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(workDir, "specs", "my-spec"), 0o755); err != nil {
			t.Fatal(err)
		}
		return &model{
			cfg:       Config{WorkDir: workDir},
			chatModel: ChatModel{Messages: make([]message, 0)},
			run:       &runState{specName: "my-spec", phase: "gating"},
		}
	}

	t.Run("names the gate and its elapsed time", func(t *testing.T) {
		m := newModel(t)
		m.reportGateHang([]GateResult{
			{Name: "build", Command: "make build", Status: GatePass},
			{Name: "test", Command: "make test", Status: GateHang, Elapsed: 90 * time.Second},
		})

		if len(m.chatModel.Messages) == 0 {
			t.Fatal("no message was appended")
		}
		got := m.chatModel.Messages[0].content
		for _, want := range []string{"test", "make test", "1m30s"} {
			if !strings.Contains(got, want) {
				t.Errorf("message does not mention %q:\n%s", want, got)
			}
		}
		// Only the hung gate is listed; a passing gate is noise here.
		if strings.Contains(got, "make build") {
			t.Errorf("message lists a gate that did not hang:\n%s", got)
		}
	})

	t.Run("marks the run failed", func(t *testing.T) {
		m := newModel(t)
		m.reportGateHang([]GateResult{{Name: "test", Command: "make test", Status: GateHang}})
		if m.run.phase != "failed" {
			t.Errorf("phase = %q, want %q", m.run.phase, "failed")
		}
	})

	t.Run("advises running the gate by hand rather than retrying", func(t *testing.T) {
		m := newModel(t)
		m.reportGateHang([]GateResult{{Name: "test", Command: "make test", Status: GateHang}})
		got := m.chatModel.Messages[0].content
		if !strings.Contains(got, "not retrying") {
			t.Errorf("message does not rule out a retry:\n%s", got)
		}
		if !strings.Contains(got, "outside the sandbox") {
			t.Errorf("message does not mention the sandbox resolution:\n%s", got)
		}
	})

	// The summary is the durable record of the run; a hang must still produce
	// one, otherwise the failure exists only in the scrollback.
	t.Run("writes a summary report", func(t *testing.T) {
		m := newModel(t)
		m.reportGateHang([]GateResult{{Name: "test", Command: "make test", Status: GateHang}})

		path := filepath.Join(m.cfg.WorkDir, "specs", "my-spec", "SUMMARY.md")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("no summary written: %v", err)
		}
		if !strings.Contains(string(data), "gate_hang") {
			t.Errorf("summary does not record the gate_hang outcome:\n%s", data)
		}
		// The summary path is announced so the reader can find the file.
		var announced bool
		for _, msg := range m.chatModel.Messages {
			if strings.Contains(msg.content, "Summary report") {
				announced = true
			}
		}
		if !announced {
			t.Error("the summary path was not announced in the transcript")
		}
	})

	// With no worktree there is nothing to preserve, so the line must be absent
	// rather than printing an empty path.
	t.Run("omits the worktree line when there is no worktree", func(t *testing.T) {
		m := newModel(t)
		m.reportGateHang([]GateResult{{Name: "test", Command: "make test", Status: GateHang}})
		if got := m.chatModel.Messages[0].content; strings.Contains(got, "Worktree preserved") {
			t.Errorf("message claims a worktree without one:\n%s", got)
		}
	})

	// No hung gate in the input still ends the run; the header must not be
	// followed by an empty list or a panic on the missing entries.
	t.Run("no hung results still reports cleanly", func(t *testing.T) {
		m := newModel(t)
		m.reportGateHang([]GateResult{{Name: "build", Command: "make build", Status: GatePass}})
		if len(m.chatModel.Messages) == 0 {
			t.Fatal("no message was appended")
		}
		if got := m.chatModel.Messages[0].content; !strings.Contains(got, "Gate hung") {
			t.Errorf("header missing:\n%s", got)
		}
		if m.run.phase != "failed" {
			t.Errorf("phase = %q, want %q", m.run.phase, "failed")
		}
	})
}

// runWorktreePath resolves the worktree a run's gates must execute in. It has to
// answer "" rather than panic for an unconfigured model, because reportGateHang
// calls it unconditionally.
func TestRunWorktreePathWithoutOrchestrator(t *testing.T) {
	tests := []struct {
		name string
		m    *model
	}{
		{name: "no orchestrator", m: &model{cfg: Config{}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.m.runWorktreePath("agent-1"); got != "" {
				t.Errorf("runWorktreePath = %q, want empty", got)
			}
			if got := tc.m.runWorktreePathsFor([]string{"agent-1"}); got != "" {
				t.Errorf("runWorktreePathsFor = %q, want empty", got)
			}
		})
	}

	// An empty agent list is not a reason to touch the orchestrator.
	m := &model{cfg: Config{}}
	if got := m.runWorktreePathsFor(nil); got != "" {
		t.Errorf("runWorktreePathsFor(nil) = %q, want empty", got)
	}
}

// retryRun returns nil when the budget is spent or the run cannot be retried at
// all. Returning a non-nil command there would spawn a cycle the run state does
// not describe.
func TestRetryRunDeclinesWhenItCannotRetry(t *testing.T) {
	t.Run("no run state", func(t *testing.T) {
		m := &model{cfg: Config{}}
		if cmd := m.retryRun("why", ""); cmd != nil {
			t.Error("retryRun with no run state returned a command")
		}
	})

	t.Run("retry budget spent", func(t *testing.T) {
		m := &model{
			cfg: Config{},
			run: &runState{retries: 2, maxRetries: 2},
		}
		if cmd := m.retryRun("why", ""); cmd != nil {
			t.Error("retryRun returned a command with the budget spent")
		}
	})

	t.Run("no orchestrator", func(t *testing.T) {
		m := &model{
			cfg: Config{},
			run: &runState{retries: 0, maxRetries: 2},
		}
		if cmd := m.retryRun("why", ""); cmd != nil {
			t.Error("retryRun returned a command without an orchestrator")
		}
	})
}
