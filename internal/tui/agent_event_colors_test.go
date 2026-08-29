package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// fgOf returns the foreground SGR sequence a rendered line opens with, so tests
// can assert two kinds render in different colors without hard-coding hex.
func fgOf(line string) string {
	if i := strings.Index(line, "m"); i > 0 && strings.HasPrefix(line, "\x1b[") {
		return line[:i+1]
	}
	return ""
}

func renderOne(t *testing.T, ev agentEv, st agentEventStyles) string {
	t.Helper()
	lines := agentEventLines(ev, st, 200)
	if len(lines) != 1 {
		t.Fatalf("kind %q rendered %d lines, want 1: %q", ev.kind, len(lines), lines)
	}
	return lines[0]
}

// TestAgentEventLines_KindsAreVisuallyDistinct pins the point of the
// stylesheet: the four roles a card mixes must not collapse into one color.
// Before this, speech, results and lifecycle events all rendered in Dim.
func TestAgentEventLines_KindsAreVisuallyDistinct(t *testing.T) {
	st := newAgentEventStyles("claude", Palette{})

	text := renderOne(t, agentEv{kind: "text", content: "hi"}, st)
	tool := renderOne(t, agentEv{kind: "tool_call", content: "git status"}, st)
	result := renderOne(t, agentEv{kind: "tool_result", content: "ok"}, st)
	thinking := renderOne(t, agentEv{kind: "thinking_delta", content: "hmm"}, st)
	failure := renderOne(t, agentEv{kind: "error", content: "spawn failed"}, st)
	lifecycle := renderOne(t, agentEv{kind: "run_done", content: ""}, st)

	roles := map[string]string{
		"text": text, "tool_call": tool, "tool_result": result,
		"thinking_delta": thinking, "error": failure, "run_done": lifecycle,
	}
	seen := make(map[string]string, len(roles))
	for name, line := range roles {
		fg := fgOf(line)
		if fg == "" {
			t.Errorf("%s rendered with no color: %q", name, line)
			continue
		}
		if prev, dup := seen[fg]; dup {
			t.Errorf("%s and %s render in the same color %q — they must be distinguishable", name, prev, fg)
		}
		seen[fg] = name
	}
}

// TestAgentEventLines_SpeechOutranksLifecycle guards the ranking rather than
// the exact hues: what the agent said must not render in the faint color
// reserved for bookkeeping.
func TestAgentEventLines_SpeechOutranksLifecycle(t *testing.T) {
	st := newAgentEventStyles("", Palette{})
	text := fgOf(renderOne(t, agentEv{kind: "text", content: "the answer"}, st))
	lifecycle := fgOf(renderOne(t, agentEv{kind: "run_done", content: ""}, st))
	if text == lifecycle {
		t.Errorf("agent speech and lifecycle noise share color %q", text)
	}
}

// TestAgentEventLines_ErrorEventIsMarked covers the event that motivated the
// change: a failed subagent arrives as kind "error" and used to fall through to
// the grey default branch, reading like ordinary bookkeeping.
func TestAgentEventLines_ErrorEventIsMarked(t *testing.T) {
	st := newAgentEventStyles("", Palette{})
	line := renderOne(t, agentEv{kind: "error", content: "acp: connection refused"}, st)
	plain := ansi.Strip(line)
	if !strings.HasPrefix(plain, "✗ ") {
		t.Errorf("error line = %q, want the ✗ marker", plain)
	}
	if !strings.Contains(plain, "connection refused") {
		t.Errorf("error line lost its message: %q", plain)
	}
	lifecycle := fgOf(renderOne(t, agentEv{kind: "run_done", content: ""}, st))
	if fgOf(line) == lifecycle {
		t.Errorf("error renders in the lifecycle color %q", lifecycle)
	}
}

// TestAgentEventLines_FailedToolResult checks a tool result that reports a
// failure is marked as one, and that a clean result is not.
func TestAgentEventLines_FailedToolResult(t *testing.T) {
	st := newAgentEventStyles("", Palette{})

	cases := []struct {
		name    string
		content string
		failed  bool
	}{
		{"json error field", `{"error":"file not found"}`, true},
		{"non-zero exit code", `{"exit_code":1,"output":"boom"}`, true},
		{"zero exit code", `{"exit_code":0,"output":"fine"}`, false},
		{"empty error field", `{"error":"","output":"fine"}`, false},
		{"plain text result", "3 files changed", false},
		{"not json", "{definitely not json", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFailedToolResult(tc.content); got != tc.failed {
				t.Fatalf("isFailedToolResult(%q) = %v, want %v", tc.content, got, tc.failed)
			}
			plain := ansi.Strip(renderOne(t, agentEv{kind: "tool_result", content: tc.content}, st))
			marker := "✓"
			if tc.failed {
				marker = "✗"
			}
			if !strings.Contains(plain, marker) {
				t.Errorf("tool_result line = %q, want marker %q", plain, marker)
			}
		})
	}
}

// TestAgentEventLines_ToolHueStillVariesByAgent keeps the one role that is
// deliberately per-agent: with several subagents running in parallel, the tool
// color is what tells their calls apart.
func TestAgentEventLines_ToolHueStillVariesByAgent(t *testing.T) {
	ev := agentEv{kind: "tool_call", content: "git status"}
	claude := fgOf(renderOne(t, ev, newAgentEventStyles("claude", Palette{})))
	gemini := fgOf(renderOne(t, ev, newAgentEventStyles("gemini", Palette{})))
	if claude == gemini {
		t.Errorf("claude and gemini tool calls share color %q", claude)
	}
}

// TestAgentEventLines_SpeechColorIsAgentIndependent is the counterpart: every
// role except the tool hue is semantic, so it must not drift per agent.
func TestAgentEventLines_SpeechColorIsAgentIndependent(t *testing.T) {
	ev := agentEv{kind: "text", content: "hi"}
	claude := fgOf(renderOne(t, ev, newAgentEventStyles("claude", Palette{})))
	gemini := fgOf(renderOne(t, ev, newAgentEventStyles("gemini", Palette{})))
	if claude != gemini {
		t.Errorf("speech color varies by agent: %q vs %q", claude, gemini)
	}
}
