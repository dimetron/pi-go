package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionStatsHandler(t *testing.T) {
	// Create a temp session directory.
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a test session with known patterns.
	sessionID := "260101-1200-aaaaa-bbbbb"
	sessionPath := filepath.Join(sessionDir, sessionID)
	if err := os.MkdirAll(sessionPath, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write events.jsonl with a mix of events.
	events := []map[string]any{
		// User message (turn 1)
		{
			"ID": "ev1", "Timestamp": time.Now().Add(-10 * time.Minute).Format(time.RFC3339Nano),
			"Author": "user", "Partial": false, "TurnComplete": false,
			"Content": map[string]any{
				"parts": []any{map[string]any{"text": "Hello, do something"}},
				"role":  "user",
			},
		},
		// Model response with tool calls (turn 1)
		{
			"ID": "ev2", "Timestamp": time.Now().Add(-9 * time.Minute).Format(time.RFC3339Nano),
			"Author": "pi", "Partial": false, "TurnComplete": true,
			"Content": map[string]any{
				"parts": []any{
					map[string]any{"functionCall": map[string]any{"name": "bash", "args": map[string]any{"command": "echo hi"}}},
					map[string]any{"functionCall": map[string]any{"name": "read", "args": map[string]any{"file_path": "test.go"}}},
				},
				"role": "model",
			},
		},
		// Tool results (turn 1)
		{
			"ID": "ev3", "Timestamp": time.Now().Add(-8 * time.Minute).Format(time.RFC3339Nano),
			"Author": "pi", "Partial": false, "TurnComplete": false,
			"Content": map[string]any{
				"parts": []any{
					map[string]any{"functionResponse": map[string]any{"name": "bash", "response": map[string]any{"stdout": "hi"}}},
					map[string]any{"functionResponse": map[string]any{"name": "read", "response": map[string]any{"content": "file content"}}},
				},
				"role": "user",
			},
		},
		// Model response with more tool calls (turn 2)
		{
			"ID": "ev4", "Timestamp": time.Now().Add(-7 * time.Minute).Format(time.RFC3339Nano),
			"Author": "pi", "Partial": false, "TurnComplete": true,
			"Content": map[string]any{
				"parts": []any{
					map[string]any{"functionCall": map[string]any{"name": "grep", "args": map[string]any{"pattern": "func"}}},
				},
				"role": "model",
			},
		},
		// Tool result (turn 2)
		{
			"ID": "ev5", "Timestamp": time.Now().Add(-6 * time.Minute).Format(time.RFC3339Nano),
			"Author": "pi", "Partial": false, "TurnComplete": false,
			"Content": map[string]any{
				"parts": []any{
					map[string]any{"functionResponse": map[string]any{"name": "grep", "response": map[string]any{"output": "matches"}}},
				},
				"role": "user",
			},
		},
		// Error event
		{
			"ID": "ev6", "Timestamp": time.Now().Add(-5 * time.Minute).Format(time.RFC3339Nano),
			"Author": "pi", "Partial": false, "TurnComplete": false,
			"ErrorCode": "INTERNAL", "ErrorMessage": "something went wrong",
			"Content": map[string]any{
				"parts": []any{map[string]any{"text": "Error occurred"}},
				"role":  "model",
			},
		},
	}

	f, err := os.Create(filepath.Join(sessionPath, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	// Run the handler.
	result, err := sessionStatsHandler(SessionStatsInput{
		SessionDir: sessionDir,
		Hours:      24,
		All:        true,
	})
	if err != nil {
		t.Fatalf("sessionStatsHandler error: %v", err)
	}

	if result.TotalSessions != 1 {
		t.Errorf("TotalSessions = %d, want 1", result.TotalSessions)
	}

	// Check that the report contains expected values.
	if !strings.Contains(result.Content, sessionID) {
		t.Errorf("report missing session ID %q", sessionID)
	}
	// The markdown summary table uses the literal header "Tool Calls";
	// assert the rendered report mentions it.
	if !strings.Contains(result.Content, "Tool Calls") {
		t.Errorf("report missing %q header: %s", "Tool Calls", result.Content)
	}
}

func TestSessionStatsHandlerEmpty(t *testing.T) {
	// Test with no sessions.
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := sessionStatsHandler(SessionStatsInput{
		SessionDir: sessionDir,
		Hours:      24,
	})
	if err != nil {
		t.Fatalf("sessionStatsHandler error: %v", err)
	}

	if result.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0", result.TotalSessions)
	}
}

func TestAnalyzeSessionFile(t *testing.T) {
	// Test the analysis function directly.
	tmpDir := t.TempDir()
	eventsPath := filepath.Join(tmpDir, "events.jsonl")

	events := []map[string]any{
		{
			"ID": "ev1", "Timestamp": time.Now().Add(-5 * time.Minute).Format(time.RFC3339Nano),
			"Author": "user", "Partial": false, "TurnComplete": false,
			"Content": map[string]any{
				"parts": []any{map[string]any{"text": "hello"}},
				"role":  "user",
			},
		},
		{
			"ID": "ev2", "Timestamp": time.Now().Add(-4 * time.Minute).Format(time.RFC3339Nano),
			"Author": "pi", "Partial": false, "TurnComplete": true,
			"Content": map[string]any{
				"parts": []any{
					map[string]any{"functionCall": map[string]any{"name": "bash", "args": map[string]any{"command": "git status"}}},
				},
				"role": "model",
			},
		},
		{
			"ID": "ev3", "Timestamp": time.Now().Add(-3 * time.Minute).Format(time.RFC3339Nano),
			"Author": "pi", "Partial": false, "TurnComplete": false,
			"Content": map[string]any{
				"parts": []any{
					map[string]any{"functionResponse": map[string]any{"name": "bash", "response": map[string]any{"stdout": "git output"}}},
				},
				"role": "user",
			},
		},
	}

	f, err := os.Create(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
	f.Close()

	stat := analyzeSessionFile("test-session", eventsPath, 20, 5)

	if stat.Lines != 3 {
		t.Errorf("Lines = %d, want 3", stat.Lines)
	}
	if stat.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", stat.ToolCalls)
	}
	if stat.ToolResults != 1 {
		t.Errorf("ToolResults = %d, want 1", stat.ToolResults)
	}
	if stat.Turns != 1 {
		t.Errorf("Turns = %d, want 1", stat.Turns)
	}
}

// TestIsGitOperation is a table-driven test for isGitOperation, exercising
// both the dedicated git-* tool names and shell invocations of `git`.
func TestIsGitOperation(t *testing.T) {
	cases := []struct {
		name string
		fc   map[string]any
		want bool
	}{
		{
			name: "git-overview",
			fc:   map[string]any{"name": "git-overview"},
			want: true,
		},
		{
			name: "git-file-diff",
			fc:   map[string]any{"name": "git-file-diff"},
			want: true,
		},
		{
			name: "git-hunk",
			fc:   map[string]any{"name": "git-hunk"},
			want: true,
		},
		{
			name: "git-status prefix",
			fc:   map[string]any{"name": "git-status"},
			want: true,
		},
		{
			name: "bash git status",
			fc: map[string]any{
				"name": "bash",
				"args": map[string]any{"command": "git status"},
			},
			want: true,
		},
		{
			name: "bash git log",
			fc: map[string]any{
				"name": "bash",
				"args": map[string]any{"command": "git log --oneline -5"},
			},
			want: true,
		},
		{
			name: "bash with leading whitespace",
			fc: map[string]any{
				"name": "bash",
				"args": map[string]any{"command": "   git status"},
			},
			want: true,
		},
		{
			name: "bash cd && git status (documented limitation)",
			fc: map[string]any{
				"name": "bash",
				"args": map[string]any{"command": "cd repo && git status"},
			},
			want: false,
		},
		{
			name: "bash ls -la",
			fc: map[string]any{
				"name": "bash",
				"args": map[string]any{"command": "ls -la"},
			},
			want: false,
		},
		{
			name: "bash missing args",
			fc:   map[string]any{"name": "bash"},
			want: false,
		},
		{
			name: "bash args missing command",
			fc: map[string]any{
				"name": "bash",
				"args": map[string]any{},
			},
			want: false,
		},
		{
			name: "read",
			fc:   map[string]any{"name": "read"},
			want: false,
		},
		{
			name: "empty name",
			fc:   map[string]any{"name": ""},
			want: false,
		},
		{
			name: "digit (no git- prefix)",
			fc:   map[string]any{"name": "digit"},
			want: false,
		},
		{
			name: "nil fc map",
			fc:   map[string]any(nil),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isGitOperation(tc.fc)
			if got != tc.want {
				t.Errorf("isGitOperation(%v) = %v, want %v", tc.fc, got, tc.want)
			}
		})
	}
}

// writeEventsJSONL is a small helper that writes a slice of event maps to
// path as JSONL, one event per line. Used by the test fixtures.
func writeEventsJSONL(t *testing.T, path string, events []map[string]any) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, ev := range events {
		if err := enc.Encode(ev); err != nil {
			t.Fatal(err)
		}
	}
}

// buildThresholdSession writes 4 tool calls / 2 turns of synthetic events to a
// fresh events.jsonl under sessionDir/<id>/. It returns the path.
func buildThresholdSession(t *testing.T, sessionDir, id string) string {
	t.Helper()
	sessionPath := filepath.Join(sessionDir, id)
	if err := os.MkdirAll(sessionPath, 0o755); err != nil {
		t.Fatal(err)
	}
	events := []map[string]any{
		{
			"ID":           "u1",
			"Timestamp":    time.Now().Add(-10 * time.Minute).Format(time.RFC3339Nano),
			"Author":       "user",
			"Partial":      false,
			"TurnComplete": false,
			"Content":      map[string]any{"parts": []any{map[string]any{"text": "do it"}}, "role": "user"},
		},
		{
			"ID":           "m1",
			"Timestamp":    time.Now().Add(-9 * time.Minute).Format(time.RFC3339Nano),
			"Author":       "pi",
			"Partial":      false,
			"TurnComplete": true,
			"Content": map[string]any{
				"parts": []any{
					map[string]any{"functionCall": map[string]any{"name": "bash", "args": map[string]any{"command": "echo a"}}},
					map[string]any{"functionCall": map[string]any{"name": "bash", "args": map[string]any{"command": "echo b"}}},
				},
				"role": "model",
			},
		},
		{
			"ID":           "u2",
			"Timestamp":    time.Now().Add(-8 * time.Minute).Format(time.RFC3339Nano),
			"Author":       "user",
			"Partial":      false,
			"TurnComplete": false,
			"Content":      map[string]any{"parts": []any{map[string]any{"text": "and also"}}, "role": "user"},
		},
		{
			"ID":           "m2",
			"Timestamp":    time.Now().Add(-7 * time.Minute).Format(time.RFC3339Nano),
			"Author":       "pi",
			"Partial":      false,
			"TurnComplete": true,
			"Content": map[string]any{
				"parts": []any{
					map[string]any{"functionCall": map[string]any{"name": "bash", "args": map[string]any{"command": "echo c"}}},
					map[string]any{"functionCall": map[string]any{"name": "bash", "args": map[string]any{"command": "echo d"}}},
				},
				"role": "model",
			},
		},
	}
	eventsPath := filepath.Join(sessionPath, "events.jsonl")
	writeEventsJSONL(t, eventsPath, events)
	return eventsPath
}

// TestSessionStatsThresholdsCustom is a regression for the threshold bug:
// with 4 tool calls / 2 turns, default thresholds (20 / 5) should not flag
// the session, but lower custom thresholds (3 / 1) must.
func TestSessionStatsThresholdsCustom(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const id = "thr-session"
	eventsPath := buildThresholdSession(t, sessionDir, id)

	// Custom thresholds: 3 tool calls, 1 turn — both should fire.
	stat := analyzeSessionFile(id, eventsPath, 3, 1)
	if !hasAnomaly(stat.Anomalies, "high tool calls (4)") {
		t.Errorf("expected anomaly %q, got %v", "high tool calls (4)", stat.Anomalies)
	}
	if !hasAnomaly(stat.Anomalies, "excessive turns (2)") {
		t.Errorf("expected anomaly %q, got %v", "excessive turns (2)", stat.Anomalies)
	}

	// Defaults: 20 tool calls, 5 turns — neither should fire.
	stat = analyzeSessionFile(id, eventsPath, 20, 5)
	if hasAnomaly(stat.Anomalies, "high tool calls (4)") {
		t.Errorf("did not expect anomaly %q with default thresholds, got %v", "high tool calls (4)", stat.Anomalies)
	}
	if hasAnomaly(stat.Anomalies, "excessive turns (2)") {
		t.Errorf("did not expect anomaly %q with default thresholds, got %v", "excessive turns (2)", stat.Anomalies)
	}

	// Handler-level: same session on disk, with custom thresholds — the
	// session should be counted as anomalous and the rendered report should
	// mention the tool-call count.
	result, err := sessionStatsHandler(SessionStatsInput{
		SessionDir:    sessionDir,
		Hours:         24,
		All:           true,
		HighToolCalls: 3,
		HighTurns:     1,
	})
	if err != nil {
		t.Fatalf("sessionStatsHandler error: %v", err)
	}
	if result.AnomalousSessions != 1 {
		t.Errorf("AnomalousSessions = %d, want 1", result.AnomalousSessions)
	}
	if !strings.Contains(result.Content, "high tool calls (4)") {
		t.Errorf("rendered content missing %q: %s", "high tool calls (4)", result.Content)
	}
}

// TestSessionStatsGitOpsAnomaly verifies the many-git-operations anomaly
// triggers at >50 git calls and not at exactly 50.
func TestSessionStatsGitOpsAnomaly(t *testing.T) {
	tmpDir := t.TempDir()

	// Helper to build a session of N `git status` bash calls.
	build := func(n int) string {
		t.Helper()
		// Just the directory name: joining tmpDir with an already-absolute id
		// produced a path containing a drive letter mid-string on Windows.
		id := "s-" + time.Now().Format("150405.000000") + "-" + itoa(n)
		sessionPath := filepath.Join(tmpDir, id)
		if err := os.MkdirAll(sessionPath, 0o755); err != nil {
			t.Fatal(err)
		}
		events := make([]map[string]any, 0, n+1)
		// One initial user message to anchor the session.
		events = append(events, map[string]any{
			"ID":           "u1",
			"Timestamp":    time.Now().Add(-time.Duration(n+1) * time.Minute).Format(time.RFC3339Nano),
			"Author":       "user",
			"Partial":      false,
			"TurnComplete": false,
			"Content":      map[string]any{"parts": []any{map[string]any{"text": "go"}}, "role": "user"},
		})
		for i := 0; i < n; i++ {
			events = append(events, map[string]any{
				"ID":           "m" + itoa(i),
				"Timestamp":    time.Now().Add(-time.Duration(n-i) * time.Minute).Format(time.RFC3339Nano),
				"Author":       "pi",
				"Partial":      false,
				"TurnComplete": i == n-1,
				"Content": map[string]any{
					"parts": []any{
						map[string]any{"functionCall": map[string]any{"name": "bash", "args": map[string]any{"command": "git status"}}},
					},
					"role": "model",
				},
			})
		}
		eventsPath := filepath.Join(sessionPath, "events.jsonl")
		writeEventsJSONL(t, eventsPath, events)
		return eventsPath
	}

	// 51 git calls: must trigger.
	high := analyzeSessionFile("high", build(51), 20, 5)
	if high.GitOps != 51 {
		t.Errorf("GitOps = %d, want 51", high.GitOps)
	}
	if !hasAnomaly(high.Anomalies, "many git operations (51)") {
		t.Errorf("expected anomaly %q, got %v", "many git operations (51)", high.Anomalies)
	}

	// 50 git calls (the threshold itself, exclusive comparison): must NOT trigger.
	boundary := analyzeSessionFile("boundary", build(50), 20, 5)
	if boundary.GitOps != 50 {
		t.Errorf("GitOps = %d, want 50", boundary.GitOps)
	}
	if hasAnomaly(boundary.Anomalies, "many git operations (50)") {
		t.Errorf("did not expect anomaly %q at the threshold, got %v", "many git operations (50)", boundary.Anomalies)
	}
}

// TestSessionStatsLongIdleSession verifies that a session with very few tool
// calls but a long elapsed duration is flagged as a long idle session.
//
// The production code measures duration as (latest event timestamp) -
// (earliest event timestamp), so we need two events spanning more than 30
// minutes between them, with very few (< 5) tool calls in between.
func TestSessionStatsLongIdleSession(t *testing.T) {
	tmpDir := t.TempDir()
	eventsPath := filepath.Join(tmpDir, "events.jsonl")
	now := time.Now()
	events := []map[string]any{
		{
			"ID":           "e1",
			"Timestamp":    now.Add(-31 * time.Minute).Format(time.RFC3339Nano),
			"Author":       "pi",
			"Partial":      false,
			"TurnComplete": true,
			"Content":      map[string]any{"parts": []any{map[string]any{"text": "hi"}}, "role": "model"},
		},
		{
			"ID":           "e2",
			"Timestamp":    now.Format(time.RFC3339Nano),
			"Author":       "pi",
			"Partial":      false,
			"TurnComplete": true,
			"Content":      map[string]any{"parts": []any{map[string]any{"text": "still here"}}, "role": "model"},
		},
	}
	writeEventsJSONL(t, eventsPath, events)

	stat := analyzeSessionFile("idle", eventsPath, 20, 5)
	if !hasAnomaly(stat.Anomalies, "long idle session") {
		t.Errorf("expected anomaly %q, got %v (duration=%s)", "long idle session", stat.Anomalies, stat.Duration)
	}
}

// TestSessionStatsArchiveSkip verifies that the top-level `archive` subdir
// is skipped when scanning session directories.
func TestSessionStatsArchiveSkip(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	keepPath := filepath.Join(sessionDir, "keep-me")
	archPath := filepath.Join(sessionDir, "archive")
	for _, p := range []string{keepPath, archPath} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Both subdirs have a fresh events.jsonl; only `keep-me` should be
	// counted because `archive` is skipped at the top level.
	writeEventsJSONL(t, filepath.Join(keepPath, "events.jsonl"), []map[string]any{
		{
			"ID":           "k1",
			"Timestamp":    time.Now().Add(-1 * time.Minute).Format(time.RFC3339Nano),
			"Author":       "pi",
			"Partial":      false,
			"TurnComplete": true,
			"Content":      map[string]any{"parts": []any{map[string]any{"text": "kept"}}, "role": "model"},
		},
	})
	writeEventsJSONL(t, filepath.Join(archPath, "events.jsonl"), []map[string]any{
		{
			"ID":           "a1",
			"Timestamp":    time.Now().Add(-1 * time.Minute).Format(time.RFC3339Nano),
			"Author":       "pi",
			"Partial":      false,
			"TurnComplete": true,
			"Content":      map[string]any{"parts": []any{map[string]any{"text": "archived"}}, "role": "model"},
		},
	})

	result, err := sessionStatsHandler(SessionStatsInput{
		SessionDir: sessionDir,
		Hours:      24,
		All:        true,
	})
	if err != nil {
		t.Fatalf("sessionStatsHandler error: %v", err)
	}
	if result.TotalSessions != 1 {
		t.Errorf("TotalSessions = %d, want 1 (archive should be skipped)", result.TotalSessions)
	}
	if !strings.Contains(result.Content, "keep-me") {
		t.Errorf("expected surviving session id %q in report, got: %s", "keep-me", result.Content)
	}
	if strings.Contains(result.Content, "archive\n") || strings.Contains(result.Content, "| archive |") {
		t.Errorf("archive session should not appear in report: %s", result.Content)
	}
}

// TestSessionStatsHoursFilter verifies that sessions whose events.jsonl has
// an mtime older than the cutoff are excluded from the report.
func TestSessionStatsHoursFilter(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "sessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(sessionDir, "old-but-here")
	if err := os.MkdirAll(oldPath, 0o755); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(oldPath, "events.jsonl")
	writeEventsJSONL(t, eventsPath, []map[string]any{
		{
			"ID":           "o1",
			"Timestamp":    time.Now().Format(time.RFC3339Nano),
			"Author":       "pi",
			"Partial":      false,
			"TurnComplete": true,
			"Content":      map[string]any{"parts": []any{map[string]any{"text": "fresh"}}, "role": "model"},
		},
	})
	// Backdate the file's mtime so the handler's mtime-based cutoff filter
	// treats it as older than the lookback window.
	old := time.Now().Add(-100 * time.Hour)
	if err := os.Chtimes(eventsPath, old, old); err != nil {
		t.Fatal(err)
	}

	// 24h window: 100h-old file is excluded.
	result, err := sessionStatsHandler(SessionStatsInput{
		SessionDir: sessionDir,
		Hours:      24,
	})
	if err != nil {
		t.Fatalf("sessionStatsHandler error: %v", err)
	}
	if result.TotalSessions != 0 {
		t.Errorf("TotalSessions = %d, want 0 with 24h lookback", result.TotalSessions)
	}

	// 200h window: same file is included.
	result, err = sessionStatsHandler(SessionStatsInput{
		SessionDir: sessionDir,
		Hours:      200,
	})
	if err != nil {
		t.Fatalf("sessionStatsHandler error: %v", err)
	}
	if result.TotalSessions != 1 {
		t.Errorf("TotalSessions = %d, want 1 with 200h lookback", result.TotalSessions)
	}
}

// TestSessionStatsErrorCountNotDoubleCounted is a regression for the
// double-count bug: a single event with ErrorCode, ErrorMessage, and
// Interrupted all set should count as exactly one error.
func TestSessionStatsErrorCountNotDoubleCounted(t *testing.T) {
	tmpDir := t.TempDir()
	eventsPath := filepath.Join(tmpDir, "events.jsonl")
	events := []map[string]any{
		{
			"ID":           "e1",
			"Timestamp":    time.Now().Add(-1 * time.Minute).Format(time.RFC3339Nano),
			"Author":       "pi",
			"Partial":      false,
			"TurnComplete": true,
			"ErrorCode":    "INTERNAL",
			"ErrorMessage": "x",
			"Interrupted":  true,
			"Content":      map[string]any{"parts": []any{map[string]any{"text": "boom"}}, "role": "model"},
		},
	}
	writeEventsJSONL(t, eventsPath, events)

	stat := analyzeSessionFile("err-session", eventsPath, 20, 5)
	if stat.Errors != 1 {
		t.Errorf("Errors = %d, want 1 (no double-count)", stat.Errors)
	}
}

// hasAnomaly reports whether the anomaly list contains target.
func hasAnomaly(anomalies []string, target string) bool {
	for _, a := range anomalies {
		if a == target {
			return true
		}
	}
	return false
}

// itoa is a tiny non-fmt-based int formatter used in test fixture IDs so the
// test file does not pull in strconv just for a handful of fixed-width IDs.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
