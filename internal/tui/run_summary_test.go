package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

// --- Summary report generation ---

func TestBuildRunSummaryReport_Completed(t *testing.T) {
	rs := &runState{
		specName:   "rate-limiter",
		agentID:    "task-42",
		phase:      "done",
		retries:    0,
		maxRetries: 3,
		startTime:  time.Now().Add(-2 * time.Minute),
		gates: []Gate{
			{Name: "build", Command: "go build ./..."},
			{Name: "test", Command: "go test ./..."},
		},
		gateResults: []GateResult{
			{Name: "build", Command: "go build ./...", Passed: true},
			{Name: "test", Command: "go test ./...", Passed: true},
		},
	}

	report := buildRunSummaryReport(rs, "completed")

	// Check header.
	if !strings.Contains(report, "# Run Summary") {
		t.Error("report should contain '# Run Summary' header")
	}

	// Check metadata.
	if !strings.Contains(report, "rate-limiter") {
		t.Error("report should contain spec name")
	}
	if !strings.Contains(report, "task-42") {
		t.Error("report should contain agent ID")
	}
	if !strings.Contains(report, "**completed**") {
		t.Error("report should contain outcome")
	}
	if !strings.Contains(report, "0 / 3") {
		t.Error("report should contain retry count")
	}
	if !strings.Contains(report, "Started") {
		t.Error("report should contain start time")
	}
	if !strings.Contains(report, "Duration") {
		t.Error("report should contain duration")
	}

	// Check gates.
	if !strings.Contains(report, "## Gates") {
		t.Error("report should contain gates section")
	}
	if !strings.Contains(report, "**PASS**") {
		t.Error("report should show PASS for passing gates")
	}
	if !strings.Contains(report, "All gates **passed**") {
		t.Error("report should state all gates passed")
	}

	// Check result.
	if !strings.Contains(report, "merged successfully") {
		t.Error("report should mention merge success for completed outcome")
	}
}

func TestBuildRunSummaryReport_GateFailed(t *testing.T) {
	rs := &runState{
		specName:   "auth-middleware",
		agentID:    "task-99",
		phase:      "failed",
		retries:    3,
		maxRetries: 3,
		startTime:  time.Now().Add(-5 * time.Minute),
		gates: []Gate{
			{Name: "build", Command: "go build ./..."},
			{Name: "test", Command: "go test ./..."},
		},
		gateResults: []GateResult{
			{Name: "build", Command: "go build ./...", Passed: true},
			{Name: "test", Command: "go test ./...", Passed: false, Output: "--- FAIL: TestAuth\n    expected 200, got 401"},
		},
	}

	report := buildRunSummaryReport(rs, "gate_failed")

	if !strings.Contains(report, "**gate_failed**") {
		t.Error("report should contain gate_failed outcome")
	}
	if !strings.Contains(report, "3 / 3") {
		t.Error("report should show max retries reached")
	}
	if !strings.Contains(report, "**FAIL**") {
		t.Error("report should show FAIL for failing gates")
	}
	if !strings.Contains(report, "FAIL: TestAuth") {
		t.Error("report should include gate failure output")
	}
	if !strings.Contains(report, "Some gates **failed**") {
		t.Error("report should state some gates failed")
	}
	if !strings.Contains(report, "after 3 retries") {
		t.Error("result should mention retries exhausted")
	}
}

func TestBuildRunSummaryReport_MergeFailed(t *testing.T) {
	rs := &runState{
		specName:   "api-refactor",
		agentID:    "task-77",
		phase:      "failed",
		retries:    0,
		maxRetries: 3,
		startTime:  time.Now().Add(-1 * time.Minute),
		gateResults: []GateResult{
			{Name: "build", Command: "go build ./...", Passed: true},
		},
	}

	report := buildRunSummaryReport(rs, "merge_failed")

	if !strings.Contains(report, "**merge_failed**") {
		t.Error("report should contain merge_failed outcome")
	}
	if !strings.Contains(report, "merge into the main branch failed") {
		t.Error("report result should mention merge failure")
	}
}

func TestBuildRunSummaryReport_NoGates(t *testing.T) {
	rs := &runState{
		specName:   "docs-update",
		agentID:    "task-10",
		phase:      "done",
		retries:    0,
		maxRetries: 3,
		startTime:  time.Now(),
	}

	report := buildRunSummaryReport(rs, "completed")

	if !strings.Contains(report, "No gates defined") {
		t.Error("report should mention no gates defined")
	}
}

func TestBuildRunSummaryReport_GatesDefinedNotExecuted(t *testing.T) {
	rs := &runState{
		specName:   "early-fail",
		agentID:    "task-5",
		phase:      "failed",
		retries:    0,
		maxRetries: 3,
		gates: []Gate{
			{Name: "build", Command: "go build ./..."},
		},
		// gateResults is nil — gates were never executed.
	}

	report := buildRunSummaryReport(rs, "gate_failed")

	if !strings.Contains(report, "Gates were defined but not executed") {
		t.Error("report should note gates were not executed")
	}
	if !strings.Contains(report, "go build ./...") {
		t.Error("report should list the defined gates")
	}
}

func TestBuildRunSummaryReport_TruncatesLongOutput(t *testing.T) {
	longOutput := strings.Repeat("x", 2000)
	rs := &runState{
		specName:   "long-output",
		agentID:    "task-1",
		retries:    1,
		maxRetries: 3,
		gateResults: []GateResult{
			{Name: "test", Command: "go test ./...", Passed: false, Output: longOutput},
		},
	}

	report := buildRunSummaryReport(rs, "gate_failed")

	if !strings.Contains(report, "...(truncated)") {
		t.Error("report should truncate long gate output")
	}
	// Report should not contain the full 2000 char string.
	if strings.Contains(report, longOutput) {
		t.Error("report should not contain the full untruncated output")
	}
}

// --- writeRunSummary integration ---

func TestWriteRunSummary_Success(t *testing.T) {
	tmpDir := t.TempDir()

	// Create spec directory.
	specDir := filepath.Join(tmpDir, "specs", "my-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	m := &model{
		cfg: Config{WorkDir: tmpDir},
		run: &runState{
			specName:   "my-feature",
			agentID:    "task-55",
			phase:      "done",
			retries:    0,
			maxRetries: 3,
			startTime:  time.Now(),
			gateResults: []GateResult{
				{Name: "build", Command: "go build ./...", Passed: true},
			},
		},
	}

	reportPath, err := m.writeRunSummary("completed")
	if err != nil {
		t.Fatalf("writeRunSummary failed: %v", err)
	}

	// Verify file exists.
	expectedPath := filepath.Join(specDir, "SUMMARY.md")
	if reportPath != expectedPath {
		t.Errorf("reportPath = %q, want %q", reportPath, expectedPath)
	}

	// Read and verify content.
	content, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("failed to read summary: %v", err)
	}
	if !strings.Contains(string(content), "# Run Summary") {
		t.Error("SUMMARY.md should contain report header")
	}
	if !strings.Contains(string(content), "my-feature") {
		t.Error("SUMMARY.md should contain spec name")
	}
	if !strings.Contains(string(content), "**completed**") {
		t.Error("SUMMARY.md should contain outcome")
	}
}

func TestWriteRunSummary_OverwritesPrevious(t *testing.T) {
	tmpDir := t.TempDir()

	specDir := filepath.Join(tmpDir, "specs", "my-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write an old summary.
	oldPath := filepath.Join(specDir, "SUMMARY.md")
	if err := os.WriteFile(oldPath, []byte("# Old Summary"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := &model{
		cfg: Config{WorkDir: tmpDir},
		run: &runState{
			specName:   "my-feature",
			agentID:    "task-new",
			phase:      "done",
			maxRetries: 3,
			startTime:  time.Now(),
		},
	}

	_, err := m.writeRunSummary("completed")
	if err != nil {
		t.Fatalf("writeRunSummary failed: %v", err)
	}

	content, err := os.ReadFile(oldPath)
	if err != nil {
		t.Fatalf("failed to read summary: %v", err)
	}
	if strings.Contains(string(content), "Old Summary") {
		t.Error("old summary should have been overwritten")
	}
	if !strings.Contains(string(content), "task-new") {
		t.Error("new summary should contain new agent ID")
	}
}

func TestWriteRunSummary_NoRunState(t *testing.T) {
	m := &model{
		cfg: Config{WorkDir: t.TempDir()},
		run: nil,
	}

	_, err := m.writeRunSummary("completed")
	if err == nil {
		t.Error("expected error with nil run state")
	}
}

func TestWriteRunSummary_MissingSpecDir(t *testing.T) {
	tmpDir := t.TempDir()
	// Don't create the specs directory.

	m := &model{
		cfg: Config{WorkDir: tmpDir},
		run: &runState{
			specName:   "nonexistent",
			agentID:    "task-1",
			maxRetries: 3,
		},
	}

	_, err := m.writeRunSummary("completed")
	if err == nil {
		t.Error("expected error when spec directory doesn't exist")
	}
}

// --- E2E: Summary report written on merge success ---

func TestE2E_MergeSuccess_WritesSummary(t *testing.T) {
	tmpDir := t.TempDir()

	// Create spec directory.
	specDir := filepath.Join(tmpDir, "specs", "rate-limiter")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	m := &model{
		cfg:      Config{WorkDir: tmpDir},
		messages: make([]message, 0),
		run: &runState{
			specName:   "rate-limiter",
			agentID:    "task-merge-ok",
			phase:      "merging",
			maxRetries: 3,
			startTime:  time.Now().Add(-30 * time.Second),
			gateResults: []GateResult{
				{Name: "build", Command: "go build ./...", Passed: true},
				{Name: "test", Command: "go test ./...", Passed: true},
			},
		},
	}

	msg := runMergeResultMsg{output: "Merge made by 'ort' strategy."}
	m.handleRunMergeResult(msg)

	// Phase should be done.
	if m.run.phase != "done" {
		t.Errorf("phase = %q, want done", m.run.phase)
	}

	// SUMMARY.md should exist.
	summaryPath := filepath.Join(specDir, "SUMMARY.md")
	content, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("SUMMARY.md not written: %v", err)
	}

	report := string(content)
	if !strings.Contains(report, "rate-limiter") {
		t.Error("summary should contain spec name")
	}
	if !strings.Contains(report, "**completed**") {
		t.Error("summary should show completed outcome")
	}
	if !strings.Contains(report, "All gates **passed**") {
		t.Error("summary should show all gates passed")
	}
	if !strings.Contains(report, "merged successfully") {
		t.Error("summary should mention merge success")
	}

	// TUI should show summary path message.
	var hasSummaryMsg bool
	for _, msg := range m.messages {
		if strings.Contains(msg.content, "Summary report") && strings.Contains(msg.content, "SUMMARY.md") {
			hasSummaryMsg = true
			break
		}
	}
	if !hasSummaryMsg {
		t.Error("expected summary report path in messages")
	}
}

// --- E2E: Summary report written on gate failure ---

func TestE2E_GateFailure_WritesSummary(t *testing.T) {
	tmpDir := t.TempDir()

	specDir := filepath.Join(tmpDir, "specs", "auth-fix")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)

	m := &model{
		cfg: Config{
			WorkDir:      tmpDir,
			Orchestrator: orch,
		},
		messages: make([]message, 0),
		run: &runState{
			specName:   "auth-fix",
			promptMD:   "# Fix Auth\n",
			agentID:    "task-gate-fail",
			phase:      "gating",
			retries:    3, // at max
			maxRetries: 3,
			startTime:  time.Now().Add(-2 * time.Minute),
		},
	}

	msg := runGateResultMsg{
		results: []GateResult{
			{Name: "build", Command: "go build ./...", Passed: true},
			{Name: "test", Command: "go test ./...", Passed: false, Output: "--- FAIL: TestAuth\n    timeout"},
		},
		passed: false,
	}

	m.handleRunGateResult(msg)

	if m.run.phase != "failed" {
		t.Errorf("phase = %q, want failed", m.run.phase)
	}

	// SUMMARY.md should exist.
	summaryPath := filepath.Join(specDir, "SUMMARY.md")
	content, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("SUMMARY.md not written: %v", err)
	}

	report := string(content)
	if !strings.Contains(report, "**gate_failed**") {
		t.Error("summary should show gate_failed outcome")
	}
	if !strings.Contains(report, "**FAIL**") {
		t.Error("summary should show FAIL for failing gate")
	}
	if !strings.Contains(report, "FAIL: TestAuth") {
		t.Error("summary should contain failure output")
	}
	if !strings.Contains(report, "3 / 3") {
		t.Error("summary should show retries exhausted")
	}
}

// --- E2E: Summary report written on merge failure ---

func TestE2E_MergeFailure_WritesSummary(t *testing.T) {
	tmpDir := t.TempDir()

	specDir := filepath.Join(tmpDir, "specs", "api-refactor")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)

	m := &model{
		cfg: Config{
			WorkDir:      tmpDir,
			Orchestrator: orch,
		},
		messages: make([]message, 0),
		run: &runState{
			specName:   "api-refactor",
			agentID:    "task-merge-fail",
			phase:      "merging",
			maxRetries: 3,
			startTime:  time.Now().Add(-1 * time.Minute),
			gateResults: []GateResult{
				{Name: "build", Command: "go build ./...", Passed: true},
			},
		},
	}

	mergeMsg := runMergeResultMsg{
		output: "CONFLICT (content): Merge conflict in handler.go",
		err:    os.ErrPermission,
	}
	m.handleRunMergeResult(mergeMsg)

	if m.run.phase != "failed" {
		t.Errorf("phase = %q, want failed", m.run.phase)
	}

	// SUMMARY.md should exist.
	summaryPath := filepath.Join(specDir, "SUMMARY.md")
	content, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("SUMMARY.md not written: %v", err)
	}

	report := string(content)
	if !strings.Contains(report, "**merge_failed**") {
		t.Error("summary should show merge_failed outcome")
	}
	if !strings.Contains(report, "merge into the main branch failed") {
		t.Error("summary result should mention merge failure")
	}
}
