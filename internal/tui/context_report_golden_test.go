package tui

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/subagent"
)

// /context is user-visible output that no unit test pins as a whole: the
// existing tests each assert one substring, so a restructure could reorder
// sections, drop a blank line or swap bold for italic and stay green. These
// goldens capture the entire rendered report — escapes included — so any
// character-level drift fails loudly.
var updateContextGolden = flag.Bool("update-context-golden", false,
	"rewrite the /context golden files from current output")

// reportTokenTracker is a fully-specified TokenTracker. The other mocks in this
// package hardcode zero for the cache and context-window accessors, which is
// exactly the half of the report the goldens exist to pin, so this one carries
// a value for every field.
type reportTokenTracker struct {
	limit          int64
	remaining      int64
	pctUsed        float64
	totalUsed      int64
	lastPromptTok  int64
	ctxWindowSize  int64
	ctxPercentUsed float64
	lastCachedTok  int64
	cachedToday    int64
	cacheHitRate   float64
	bodyTokens     int64
	cachePrefixTok int64
}

func (f reportTokenTracker) Limit() int64                { return f.limit }
func (f reportTokenTracker) Remaining() int64            { return f.remaining }
func (f reportTokenTracker) PercentUsed() float64        { return f.pctUsed }
func (f reportTokenTracker) TotalUsed() int64            { return f.totalUsed }
func (f reportTokenTracker) LastPromptTokens() int64     { return f.lastPromptTok }
func (f reportTokenTracker) SetLastPromptTokens(int64)   {}
func (f reportTokenTracker) ResetContextWindow()         {}
func (f reportTokenTracker) ContextWindowSize() int64    { return f.ctxWindowSize }
func (f reportTokenTracker) ContextPercentUsed() float64 { return f.ctxPercentUsed }
func (f reportTokenTracker) LastCachedTokens() int64     { return f.lastCachedTok }
func (f reportTokenTracker) CachedTokensToday() int64    { return f.cachedToday }
func (f reportTokenTracker) CacheHitRateToday() float64  { return f.cacheHitRate }
func (f reportTokenTracker) BodyTokens() int64           { return f.bodyTokens }
func (f reportTokenTracker) CachePrefixTokens() int64    { return f.cachePrefixTok }

type reportCompactMetrics struct{}

func (reportCompactMetrics) FormatStats() string {
	return "- **Compacted**: 3 outputs, 12.4k tokens saved\n"
}

// contextReportModel builds a session with every optional section populated:
// breakdown, tracker, skills, subagents and compaction metrics. Sections are
// only emitted when their input is present, so a fixture missing one leaves
// that writer unpinned.
func contextReportModel(t *testing.T) *model {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	for id, status := range map[string]string{
		"agent-a": "running",
		"agent-b": "done",
		"agent-c": "failed",
	} {
		if !orch.SetStatusForTest(id, status) {
			t.Fatalf("SetStatusForTest(%q) refused", id)
		}
	}

	return &model{
		ctx:    ctx,
		cancel: cancel,
		width:  100,
		height: 40,
		// A real palette, not the zero value: the breakdown renders through
		// lipgloss, and an unset palette emits no escapes at all — which would
		// leave the styled half of the report untested.
		palette: paletteFor(Theme{ThemeType: "dark"}),
		cfg: Config{
			ModelName:    "claude-opus-5",
			ProviderName: "anthropic",
			TokenTracker: reportTokenTracker{
				limit:          500_000,
				remaining:      380_000,
				pctUsed:        24,
				totalUsed:      120_000,
				lastPromptTok:  48_000,
				ctxWindowSize:  200_000,
				ctxPercentUsed: 24,
				lastCachedTok:  36_000,
				cachedToday:    90_000,
				cacheHitRate:   62.5,
				bodyTokens:     12_000,
				cachePrefixTok: 30_000,
			},
			ContextBreakdown: &ContextBreakdown{
				SystemPrompt: 2_400,
				ToolDefs:     8_100,
				Rules:        1_900,
				Skills:       3_300,
				MCPTools:     5_600,
				Subagents:    1_200,
			},
			// Deliberately unsorted, with an empty Source and an empty
			// Description, so the listing's sort and both fallbacks are pinned.
			Skills: []extension.Skill{
				{Name: "zoe-html", Description: "code screenshots", Source: "project"},
				{Name: "go-testing", Description: "", Source: ""},
				{Name: "go-pprof", Description: "profile a process", Source: "bundled"},
			},
			Orchestrator:   orch,
			CompactMetrics: reportCompactMetrics{},
		},
		chatModel: ChatModel{Messages: []message{
			{role: "user", content: strings.Repeat("u", 1_200)},
			{role: "assistant", content: strings.Repeat("a", 3_400)},
			{role: "tool", tool: "bash", toolIn: "ls -la", content: strings.Repeat("t", 900)},
			{role: "user", content: "and then?"},
			{role: "assistant", content: strings.Repeat("a", 700)},
		}},
	}
}

func TestFormatContextUsageGolden(t *testing.T) {
	tests := []struct {
		name   string
		golden string
		setup  func(*model)
	}{
		{
			name:   "full session",
			golden: "context_report_full.golden",
		},
		{
			// Everything the tracker feeds — the token line, daily usage, the
			// context window and the cache — has a separate no-tracker path.
			name:   "no token tracker",
			golden: "context_report_no_tracker.golden",
			setup:  func(m *model) { m.cfg.TokenTracker = nil },
		},
		{
			// The breakdown is the newest section and the only one that can be
			// absent while a tracker is present; without it the report starts
			// at the legacy estimate.
			name:   "no breakdown",
			golden: "context_report_no_breakdown.golden",
			setup:  func(m *model) { m.cfg.ContextBreakdown = nil },
		},
		{
			// A tracker that has seen one response but knows nothing else:
			// no daily total, no window size, no cache hit. Each of those is a
			// separate degraded wording the full-session golden never reaches.
			name:   "tracker without window or cache",
			golden: "context_report_sparse_tracker.golden",
			setup: func(m *model) {
				m.cfg.TokenTracker = reportTokenTracker{
					limit:         500_000,
					remaining:     500_000,
					lastPromptTok: 48_000,
				}
			},
		},
		{
			// A fresh session: no messages, no tracker, no extensions. This is
			// what /context prints before the first turn.
			name:   "bare session",
			golden: "context_report_bare.golden",
			setup: func(m *model) {
				m.cfg.TokenTracker = nil
				m.cfg.ContextBreakdown = nil
				m.cfg.Skills = nil
				m.cfg.Orchestrator = nil
				m.cfg.CompactMetrics = nil
				m.chatModel.Messages = nil
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := contextReportModel(t)
			if tc.setup != nil {
				tc.setup(m)
			}
			assertGolden(t, tc.golden, m.formatContextUsage())
		})
	}
}

func assertGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)
	if *updateContextGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s (regenerate with -update-context-golden): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch\n--- got ---\n%q\n--- want ---\n%q", name, got, want)
	}
}
