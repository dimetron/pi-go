package tui

import (
	"strings"
	"testing"
	"time"
)

// TestStatusFieldsEmpty pins which fields decline a segment. Render skips any
// field that returns "", so an empty string is how a field says "nothing to
// report" — a field that returned a styled blank would add a stray separator.
func TestStatusFieldsEmpty(t *testing.T) {
	t.Parallel()

	p := paletteOrDark(Palette{})

	tests := []struct {
		name  string
		field string
	}{
		{"no pending prompts", queueField(0, p)},
		{"negative pending", queueField(-1, p)},
		{"no token tracker", tokenField(nil, p)},
		{"tracker with no limit and no usage", tokenField(&cmdMockTokenTracker{}, p)},
		{"neither folder nor host", locationField(StatusRenderInput{}, p)},
		{"no active tools", (&StatusModel{}).toolsField(p)},
		{"no run cycle", runCycleField(nil, p)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.field != "" {
				t.Errorf("field = %q, want empty", tt.field)
			}
		})
	}
}

// TestStatusFieldsContent checks the text each field contributes, ignoring the
// styling around it.
func TestStatusFieldsContent(t *testing.T) {
	t.Parallel()

	p := paletteOrDark(Palette{})

	tests := []struct {
		name  string
		field string
		want  string
	}{
		{"queued count", queueField(3, p), "queued: 3"},
		{"tokens against a limit", tokenField(&cmdMockTokenTracker{limit: 100000, totalUsed: 42000, percentUsed: 42}, p), "tkn: 42.0k/100.0k"},
		{"tokens over the limit", tokenField(&cmdMockTokenTracker{limit: 100, totalUsed: 120, percentUsed: 120}, p), "tkn: 120/100"},
		{"tokens without a limit", tokenField(&cmdMockTokenTracker{totalUsed: 900}, p), "tkn: 900"},
		{"context bar from a tracker", contextField(StatusRenderInput{TokenTracker: &cmdMockTokenTracker{limit: 100, totalUsed: 42, percentUsed: 42}}, p), "████░░░░░░ 42%"},
		{"context estimate without a tracker", contextField(StatusRenderInput{}, p), "ctx: 0"},
		{"folder only", locationField(StatusRenderInput{FolderName: "pi-go"}, p), "pi-go"},
		{"host only", locationField(StatusRenderInput{HostName: "box"}, p), "box"},
		{"folder and host", locationField(StatusRenderInput{FolderName: "pi-go", HostName: "box"}, p), "pi-go | box"},
		{"run cycle", runCycleField(&runCycleInfo{Cycle: 2, MaxRetries: 5}, p), "cycle 2/5"},
		{"loading items sort and tick", loadingField(map[string]bool{"mcp": true, "lsp": false}, p), "load: lsp... mcp ✓"},
		{
			"parallel tools are named in sorted order",
			(&StatusModel{ActiveTools: map[string]time.Time{"read": {}, "bash": {}}}).toolsField(p),
			"tools[2]: bash, read",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := stripANSI(tt.field); got != tt.want {
				t.Errorf("field = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStatusToolsFieldSingle covers the single-tool branch, whose elapsed time
// makes an exact comparison useless.
func TestStatusToolsFieldSingle(t *testing.T) {
	t.Parallel()

	s := &StatusModel{ActiveTool: "bash", ToolStart: time.Now()}
	got := stripANSI(s.toolsField(paletteOrDark(Palette{})))
	if !strings.HasPrefix(got, "tool: bash (") || !strings.HasSuffix(got, ")") {
		t.Errorf("toolsField() = %q, want \"tool: bash (<elapsed>)\"", got)
	}
}

// TestStatusModeField covers the bracketed field's precedence: a flash borrows
// the slot from everything else, plan mode outranks the spinner, and the
// spinner only runs when no tool is active.
func TestStatusModeField(t *testing.T) {
	t.Parallel()

	p := paletteOrDark(Palette{})

	tests := []struct {
		name  string
		model StatusModel
		in    StatusRenderInput
		want  string
	}{
		{"empty mode defaults to chat", StatusModel{}, StatusRenderInput{}, "chat"},
		{"plan mode", StatusModel{}, StatusRenderInput{Mode: "plan"}, "plan"},
		{"flash outranks plan", StatusModel{}, StatusRenderInput{Mode: "plan", Flash: "Copied!"}, "Copied!"},
		{"flash outranks the spinner", StatusModel{}, StatusRenderInput{Running: true, Flash: "Copied!"}, "Copied!"},
		{"running with an active tool keeps the mode", StatusModel{ActiveTool: "bash"}, StatusRenderInput{Running: true}, "chat"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := strings.TrimSpace(stripANSI(tt.model.modeField(tt.in, p)))
			got = strings.TrimSpace(strings.Trim(got, "[]"))
			if got != tt.want {
				t.Errorf("modeField() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStatusRenderSkipsEmptyFields checks that Render adds a separator only for
// fields that reported something. A bar for an empty model carries the mode and
// the context estimate and nothing else.
func TestStatusRenderSkipsEmptyFields(t *testing.T) {
	t.Parallel()

	s := &StatusModel{Width: 120}
	got := stripANSI(s.Render(StatusRenderInput{}))
	if n := strings.Count(got, "│"); n != 1 {
		t.Errorf("Render() has %d separators, want 1; got %q", n, got)
	}

	full := stripANSI(s.Render(StatusRenderInput{
		Pending:    2,
		FolderName: "pi-go",
		HostName:   "box",
		RunCycle:   &runCycleInfo{Cycle: 1, MaxRetries: 3},
	}))
	for _, want := range []string{"queued: 2", "ctx: 0", "pi-go | box", "cycle 1/3"} {
		if !strings.Contains(full, want) {
			t.Errorf("Render() = %q, missing %q", full, want)
		}
	}
	if n := strings.Count(full, "│"); n != 4 {
		t.Errorf("Render() has %d separators, want 4; got %q", n, full)
	}
}

// TestStatusRenderLoading checks that loading progress replaces the normal
// content rather than joining it.
func TestStatusRenderLoading(t *testing.T) {
	t.Parallel()

	s := &StatusModel{Width: 120}
	got := stripANSI(s.Render(StatusRenderInput{
		Pending:      5,
		FolderName:   "pi-go",
		LoadingItems: map[string]bool{"mcp": true, "lsp": false},
	}))
	if !strings.Contains(got, "load: lsp... mcp ✓") {
		t.Errorf("Render() = %q, want the loading field", got)
	}
	for _, unwanted := range []string{"queued: 5", "pi-go", "ctx:"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("Render() = %q, should not carry %q while loading", got, unwanted)
		}
	}
}
