package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestNewBashTool ensures newBashTool constructs successfully and is registered
// with the correct name and description.
func TestNewBashTool(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	tool, err := newBashTool(sb)
	if err != nil {
		t.Fatalf("newBashTool: %v", err)
	}
	if tool == nil {
		t.Fatal("newBashTool returned nil tool")
	}
	if tool.Name() != "bash" {
		t.Errorf("tool.Name() = %q, want %q", tool.Name(), "bash")
	}
}

// TestBashHandler_TimeoutCappedAtTenMinutes covers the branch that caps the
// user-supplied timeout at 10 minutes. We pass a value > 10 minutes and then
// verify a near-instant command still succeeds (so we don't actually wait).
func TestBashHandler_TimeoutCappedAtTenMinutes(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Timeout of 20 minutes should be capped at 10 minutes.
	// The command itself completes in a fraction of a second.
	out, err := bashHandler(sb, nil, BashInput{Command: "echo capped", Timeout: 20 * 60 * 1000})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", out.ExitCode)
	}
	if !strings.Contains(out.Stdout, "capped") {
		t.Errorf("expected stdout to contain 'capped', got %q", out.Stdout)
	}
}

// TestBashHandler_TimeoutZeroUsesDefault covers the path where Timeout == 0
// falls through to the default timeout.
func TestBashHandler_TimeoutZeroUsesDefault(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	out, err := bashHandler(sb, nil, BashInput{Command: "echo default", Timeout: 0})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if out.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", out.ExitCode)
	}
}

// TestBashHandler_SigpipeTreatedAsSuccess covers the SIGPIPE (exit 141)
// branch where exit code is rewritten to 0.
func TestBashHandler_SigpipeTreatedAsSuccess(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// yes will receive SIGPIPE when head closes the pipe after 1 line.
	// Bash reports the producer exit status as 141 (128 + SIGPIPE=13).
	// The `pipefail` option surfaces SIGPIPE to the shell's exit code.
	out, err := bashHandler(sb, nil, BashInput{
		Command: "set -o pipefail; yes | head -n 1",
	})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	// The handler should rewrite 141 to 0 (success).
	if out.ExitCode != 0 {
		t.Errorf("expected SIGPIPE exit 141 to be rewritten to 0, got %d", out.ExitCode)
	}
	if !strings.HasPrefix(out.Stdout, "y") {
		t.Errorf("expected stdout to start with 'y', got %q", out.Stdout)
	}
}

// TestBashHandler_SecretsRedacted verifies the bash output integrates with
// redactSecrets so leaked API keys in stdout are masked.
func TestBashHandler_SecretsRedacted(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	out, err := bashHandler(sb, nil, BashInput{
		Command: "echo export API_KEY=sk-abcdefghijklmnopqrstuvwxyz1234",
	})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if strings.Contains(out.Stdout, "sk-abcdefghijklmnopqrstuvwxyz1234") {
		t.Errorf("expected sk- secret to be redacted in stdout, got %q", out.Stdout)
	}
	if !strings.Contains(out.Stdout, "***") {
		t.Errorf("expected '***' in stdout, got %q", out.Stdout)
	}
}

// TestBashHandler_RunsInSandboxDir verifies that the command runs with Dir
// set to the sandbox root.
func TestBashHandler_RunsInSandboxDir(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Write a file in the sandbox and cat it via relative path.
	marker := filepath.Join(dir, "marker.txt")
	if err := os.WriteFile(marker, []byte("found-me"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	out, err := bashHandler(sb, nil, BashInput{Command: "cat marker.txt"})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("exit code %d, stderr: %s", out.ExitCode, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "found-me") {
		t.Errorf("expected stdout to contain 'found-me', got %q", out.Stdout)
	}
}

// TestBashHandler_TimeoutExact confirms a command that runs longer than the
// given timeout reports a non-zero exit code. Depending on the system,
// timed-out bash may surface as either ExitCode=-1 (DeadlineExceeded branch)
// or the SIGKILL exit status via ExitError, so we only assert non-zero.
func TestBashHandler_TimeoutExact(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	out, err := bashHandler(sb, nil, BashInput{
		Command: "sleep 5",
		Timeout: 200, // 200 ms
	})
	if err != nil {
		t.Fatalf("bashHandler returned error: %v", err)
	}
	if out.ExitCode == 0 {
		t.Errorf("expected non-zero ExitCode for timeout, got %d", out.ExitCode)
	}
}

// TestBashHandler_StdoutAndStderrAreSeparated verifies that output written to
// stdout and stderr land in the correct BashOutput fields and do not bleed into
// each other. This guards against the class of bug seen in Jaeger traces where
// tool_response showed tool_call_args (input echoed as output).
func TestBashHandler_StdoutAndStderrAreSeparated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	out, err := bashHandler(sb, nil, BashInput{
		Command: "echo stdout-marker; echo stderr-marker >&2",
	})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if out.ExitCode != 0 {
		t.Fatalf("exit code %d, stderr: %s", out.ExitCode, out.Stderr)
	}
	if !strings.Contains(out.Stdout, "stdout-marker") {
		t.Errorf("stdout missing stdout-marker: %q", out.Stdout)
	}
	if strings.Contains(out.Stdout, "stderr-marker") {
		t.Errorf("stdout must not contain stderr-marker: %q", out.Stdout)
	}
	if !strings.Contains(out.Stderr, "stderr-marker") {
		t.Errorf("stderr missing stderr-marker: %q", out.Stderr)
	}
	if strings.Contains(out.Stderr, "stdout-marker") {
		t.Errorf("stderr must not contain stdout-marker: %q", out.Stderr)
	}
}

// TestBashHandler_OutputIsNotInput verifies that the command output (stdout)
// does not contain the input command text. The Jaeger trace anomaly showed
// tool_response == tool_call_args; this test catches a regression where the
// handler could return args instead of actual output.
func TestBashHandler_OutputIsNotInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	cmd := "echo unique-output-value-xyz"
	out, err := bashHandler(sb, nil, BashInput{Command: cmd})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if strings.Contains(out.Stdout, cmd) {
		t.Errorf("stdout must not contain input command %q, got: %q", cmd, out.Stdout)
	}
	if !strings.Contains(out.Stdout, "unique-output-value-xyz") {
		t.Errorf("stdout must contain command output, got: %q", out.Stdout)
	}
}

// TestBashHandler_ConcurrentCallsProduceIndependentOutput runs N bash
// invocations in parallel and verifies each gets its own independent
// stdout/stderr/exit-code — no buffer sharing, no output cross-contamination.
func TestBashHandler_ConcurrentCallsProduceIndependentOutput(t *testing.T) {
	t.Parallel()
	const workers = 20
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	type result struct {
		id  int
		out BashOutput
		err error
	}
	results := make([]result, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			out, err := bashHandler(sb, nil, BashInput{
				Command: fmt.Sprintf("echo worker-%d-stdout; echo worker-%d-stderr >&2", i, i),
			})
			results[i] = result{id: i, out: out, err: err}
		}()
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			t.Errorf("worker %d: unexpected error: %v", r.id, r.err)
			continue
		}
		if r.out.ExitCode != 0 {
			t.Errorf("worker %d: exit code %d", r.id, r.out.ExitCode)
		}
		wantOut := fmt.Sprintf("worker-%d-stdout", r.id)
		wantErr := fmt.Sprintf("worker-%d-stderr", r.id)
		if !strings.Contains(r.out.Stdout, wantOut) {
			t.Errorf("worker %d: stdout = %q, want %q", r.id, r.out.Stdout, wantOut)
		}
		if !strings.Contains(r.out.Stderr, wantErr) {
			t.Errorf("worker %d: stderr = %q, want %q", r.id, r.out.Stderr, wantErr)
		}
		// Verify no cross-contamination from other workers
		for j := 0; j < workers; j++ {
			if j == r.id {
				continue
			}
			if strings.Contains(r.out.Stdout, fmt.Sprintf("worker-%d-stdout", j)) {
				t.Errorf("worker %d stdout contaminated by worker %d output", r.id, j)
			}
		}
	}
}

// TestBashHandler_BothStreamsAndExitCode verifies that a command writing to
// both stdout and stderr with a non-zero exit code populates all three fields
// of BashOutput independently.
func TestBashHandler_BothStreamsAndExitCode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	out, err := bashHandler(sb, nil, BashInput{
		Command: "echo out-line; echo err-line >&2; exit 7",
	})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if out.ExitCode != 7 {
		t.Errorf("ExitCode = %d, want 7", out.ExitCode)
	}
	if !strings.Contains(out.Stdout, "out-line") {
		t.Errorf("Stdout = %q, want 'out-line'", out.Stdout)
	}
	if !strings.Contains(out.Stderr, "err-line") {
		t.Errorf("Stderr = %q, want 'err-line'", out.Stderr)
	}
}
