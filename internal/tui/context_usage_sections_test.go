package tui

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/subagent"
)

func TestEstimateRoleTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msgs []message
		want roleTokens
	}{
		{
			name: "empty transcript",
			msgs: nil,
			want: roleTokens{},
		},
		{
			name: "user only",
			msgs: []message{{role: "user", content: strings.Repeat("u", 400)}},
			want: roleTokens{user: 100, total: 100},
		},
		{
			name: "one of each role",
			msgs: []message{
				{role: "user", content: strings.Repeat("u", 400)},
				{role: "assistant", content: strings.Repeat("a", 800)},
				{role: "tool", content: strings.Repeat("t", 1200)},
			},
			want: roleTokens{user: 100, assistant: 200, tool: 300, total: 600},
		},
		{
			name: "unrecognized roles count as tool output",
			msgs: []message{
				{role: "thinking", content: strings.Repeat("x", 400)},
				{role: "", content: strings.Repeat("y", 400)},
			},
			want: roleTokens{tool: 200, total: 200},
		},
		{
			// The total is estimated from the combined character count, so it
			// does not have to equal the sum of the three rounded parts.
			name: "total is not the sum of the rounded parts",
			msgs: []message{
				{role: "user", content: "aa"},
				{role: "assistant", content: "bb"},
				{role: "tool", content: "cc"},
			},
			want: roleTokens{user: 0, assistant: 0, tool: 0, total: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := estimateRoleTokens(tt.msgs); got != tt.want {
				t.Errorf("estimateRoleTokens() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCountAgentStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                        string
		statuses                    []string
		running, done, wantedFailed int
	}{
		{name: "none", statuses: nil},
		{name: "all running", statuses: []string{"running", "running"}, running: 2},
		{name: "all failed", statuses: []string{"failed"}, wantedFailed: 1},
		{
			// Anything that is neither running nor failed is done: completed,
			// canceled, killed, timeout, and the zero value alike.
			name:     "everything else counts as done",
			statuses: []string{"completed", "canceled", "killed", "timeout", ""},
			done:     5,
		},
		{
			name:         "mixed",
			statuses:     []string{"running", "completed", "failed", "running", "timeout"},
			running:      2,
			done:         2,
			wantedFailed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			agents := make([]subagent.AgentStatus, len(tt.statuses))
			for i, s := range tt.statuses {
				agents[i] = subagent.AgentStatus{Status: s}
			}

			running, done, failed := countAgentStatuses(agents)
			if running != tt.running || done != tt.done || failed != tt.wantedFailed {
				t.Errorf("countAgentStatuses() = (running %d, done %d, failed %d), want (%d, %d, %d)",
					running, done, failed, tt.running, tt.done, tt.wantedFailed)
			}
		})
	}
}

func TestContextCacheSection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		tracker      fakeTokenTracker
		promptTokens int64
		want         []string
		notWant      []string
	}{
		{
			name:         "no cache activity still reports",
			tracker:      fakeTokenTracker{},
			promptTokens: 10_000,
			want:         []string{"*Prompt cache*", "- **Last request**: no cache hit\n"},
			notWant:      []string{"**Today**", "**Stable prefix**"},
		},
		{
			name:         "hit reports its share of the prompt",
			tracker:      fakeTokenTracker{lastCachedTok: 5_000},
			promptTokens: 10_000,
			want:         []string{"- **Last request**: 5.0k of 10.0k prompt tokens cached (50%)"},
		},
		{
			name:         "daily line appears only above zero",
			tracker:      fakeTokenTracker{cachedToday: 123_456, cacheHitRate: 42.5},
			promptTokens: 10_000,
			want:         []string{"- **Today**: 123.5k tokens read from cache (42% of input)"},
		},
		{
			name:         "stable prefix pairs with the body size",
			tracker:      fakeTokenTracker{cachePrefixTok: 15_679, bodyTokens: 4_321},
			promptTokens: 10_000,
			want:         []string{"- **Stable prefix**: 15.7k tokens · **body since**: 4.3k tokens"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := contextCacheSection(tt.tracker, tt.promptTokens)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("unexpected %q in:\n%s", w, got)
				}
			}
		})
	}
}

// Each section owns one question and must stay silent when it has no answer —
// otherwise /context grows empty headers as subsystems go missing.
func TestContextSectionsAreEmptyWhenTheyHaveNothingToSay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     Config
		section func(*model) string
	}{
		{
			name:    "breakdown without a measurement",
			cfg:     Config{},
			section: (*model).contextBreakdownSection,
		},
		{
			name:    "daily usage without a tracker",
			cfg:     Config{},
			section: (*model).contextDailySection,
		},
		{
			name:    "daily usage with a tracker that has spent nothing",
			cfg:     Config{TokenTracker: fakeTokenTracker{limit: 100_000}},
			section: (*model).contextDailySection,
		},
		{
			name:    "context window without a tracker",
			cfg:     Config{},
			section: (*model).contextWindowSection,
		},
		{
			name:    "context window before the first response",
			cfg:     Config{TokenTracker: fakeTokenTracker{totalUsed: 500}},
			section: (*model).contextWindowSection,
		},
		{
			name:    "skills when none are loaded",
			cfg:     Config{},
			section: (*model).contextSkillsSection,
		},
		{
			name:    "subagents without an orchestrator",
			cfg:     Config{},
			section: (*model).contextSubagentSection,
		},
		{
			name:    "subagents when none have been spawned",
			cfg:     Config{Orchestrator: subagent.NewOrchestrator(&config.Config{}, "", nil)},
			section: (*model).contextSubagentSection,
		},
		{
			name:    "compaction without metrics",
			cfg:     Config{},
			section: (*model).contextCompactionSection,
		},
		{
			name:    "compaction with empty stats",
			cfg:     Config{CompactMetrics: &mockCompactStats{stats: ""}},
			section: (*model).contextCompactionSection,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &model{width: 120, cfg: tt.cfg}
			if got := tt.section(m); got != "" {
				t.Errorf("section rendered %q, want empty", got)
			}
		})
	}
}

func TestContextSkillsSection(t *testing.T) {
	t.Parallel()

	m := &model{
		width: 120,
		cfg: Config{Skills: []extension.Skill{
			{Name: "zebra", Description: "Last alphabetically", Source: "project"},
			{Name: "alpha", Description: "First skill", Source: "bundled"},
			{Name: "bare"},
		}},
	}

	got := m.contextSkillsSection()

	if !strings.Contains(got, "(3 loaded)") {
		t.Errorf("missing the loaded count in:\n%s", got)
	}
	// A missing source reads as a user skill, a missing description says so
	// explicitly, and an unread body is called out rather than reported as 0.
	if !strings.Contains(got, "- /bare — (no description) [user]  body: not loaded") {
		t.Errorf("missing the fallback line in:\n%s", got)
	}
	if alpha, zebra := strings.Index(got, "/alpha"), strings.Index(got, "/zebra"); alpha > zebra {
		t.Errorf("skills are not alphabetical: alpha at %d, zebra at %d", alpha, zebra)
	}
}

func TestContextHeadlineSection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cfg         Config
		totalTokens int64
		want        string
	}{
		{
			name:        "no tracker and no conversation leaves the bar empty",
			cfg:         Config{ModelName: "m"},
			totalTokens: 0,
			want:        "`░░░░░░░░░░░░░░░░░░░░`  m · ctx ~0 tokens",
		},
		{
			// Under the 10k threshold the bar is floored at one block so a short
			// conversation still reads as "something is in here".
			name:        "short conversation floors at one block",
			cfg:         Config{ModelName: "m"},
			totalTokens: 500,
			want:        "`█░░░░░░░░░░░░░░░░░░░`  m · ctx ~500 tokens",
		},
		{
			name:        "long conversation scales against a nominal 100k",
			cfg:         Config{ModelName: "m"},
			totalTokens: 50_000,
			want:        "`██████████░░░░░░░░░░`  m · ctx ~50.0k tokens",
		},
		{
			name: "a daily limit switches the line to used/limit",
			cfg: Config{
				ModelName:    "m",
				ProviderName: "p",
				TokenTracker: fakeTokenTracker{limit: 100_000, totalUsed: 25_000, pctUsed: 25},
			},
			totalTokens: 500,
			want:        "`█████░░░░░░░░░░░░░░░`  p | m · 25.0k/100.0k tokens (25%)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &model{width: 120, cfg: tt.cfg}
			got := m.contextHeadlineSection(tt.totalTokens)
			if !strings.HasPrefix(got, "**Context Usage**\n\n") {
				t.Errorf("missing the header in:\n%s", got)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("missing %q in:\n%s", tt.want, got)
			}
		})
	}
}

func TestContextWindowSection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tracker fakeTokenTracker
		want    []string
	}{
		{
			name:    "unknown window falls back to the raw prompt size",
			tracker: fakeTokenTracker{lastPromptTok: 7_500},
			want:    []string{"- **Last prompt**: 7.5k tokens (window size unknown)"},
		},
		{
			name:    "known window reports used and free",
			tracker: fakeTokenTracker{lastPromptTok: 20_000, ctxWindowSize: 200_000, ctxPercentUsed: 10},
			want: []string{
				"`██░░░░░░░░░░░░░░░░░░`  20.0k / 200.0k (10%)",
				"- **Used**: 20.0k tokens",
				"- **Free**: 180.0k tokens (90%)",
			},
		},
		{
			// A prompt bigger than the window clamps free at zero rather than
			// printing a negative token count.
			name:    "an overfull window clamps free tokens at zero",
			tracker: fakeTokenTracker{lastPromptTok: 250_000, ctxWindowSize: 200_000, ctxPercentUsed: 125},
			want:    []string{"- **Free**: 0 tokens (-25%)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &model{width: 120, cfg: Config{TokenTracker: tt.tracker}}
			got := m.contextWindowSection()
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			// The cache lines always follow the window, on every branch.
			if !strings.Contains(got, "*Prompt cache*") {
				t.Errorf("missing the prompt cache section in:\n%s", got)
			}
		})
	}
}
