package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/subagent"
)

// These tests pin the behavior the cyclomatic-complexity refactor moved out of
// handleSlashCommand, slashCommandDesc, formatContextUsage, handleRunCommand,
// buildRunSummaryReport, handleRunAgentEvent, View, updateTerminal,
// renderSearchPopup and handleSearchPopupKey. Each extracted helper is
// exercised at the boundaries its original branch encoded.

// cplxModel builds a model that is usable by every helper under test here.
func cplxModel(t *testing.T) *model {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &model{
		cfg: Config{
			ModelName:    "test-model",
			ProviderName: "test-provider",
			SessionID:    "sess-1",
			WorkDir:      t.TempDir(),
		},
		ctx:        ctx,
		cancel:     cancel,
		inputModel: NewInputModel(make([]HistoryEntry, 0), nil, nil, ""),
		chatModel:  ChatModel{Messages: make([]message, 0)},
		face:       NewFaceRenderer(),
		palette:    darkPalette,
		width:      100,
		height:     30,
	}
}

// cplxTracker is a fully settable TokenTracker, so the /context branches the
// package's existing mock hardcodes to zero can be reached.
type cplxTracker struct {
	limit       int64
	remaining   int64
	percentUsed float64
	totalUsed   int64
	lastPrompt  int64
	window      int64
	ctxPercent  float64
	lastCached  int64
	cachedToday int64
	hitRate     float64
	body        int64
	prefix      int64
}

func (c *cplxTracker) Limit() int64                { return c.limit }
func (c *cplxTracker) Remaining() int64            { return c.remaining }
func (c *cplxTracker) PercentUsed() float64        { return c.percentUsed }
func (c *cplxTracker) TotalUsed() int64            { return c.totalUsed }
func (c *cplxTracker) LastPromptTokens() int64     { return c.lastPrompt }
func (c *cplxTracker) ContextWindowSize() int64    { return c.window }
func (c *cplxTracker) ContextPercentUsed() float64 { return c.ctxPercent }
func (c *cplxTracker) SetLastPromptTokens(n int64) { c.lastPrompt = n }
func (c *cplxTracker) ResetContextWindow()         { c.lastPrompt = 0 }
func (c *cplxTracker) LastCachedTokens() int64     { return c.lastCached }
func (c *cplxTracker) CachedTokensToday() int64    { return c.cachedToday }
func (c *cplxTracker) CacheHitRateToday() float64  { return c.hitRate }
func (c *cplxTracker) BodyTokens() int64           { return c.body }
func (c *cplxTracker) CachePrefixTokens() int64    { return c.prefix }

// -----------------------------------------------------------------------------
// Slash command table — the shared source of truth behind handleSlashCommand,
// slashCommands and slashCommandDesc.
// -----------------------------------------------------------------------------

// The dispatch switch and the description switch used to enumerate the command
// set separately. Merging them is only safe if the table still covers exactly
// what both did, so pin the full set explicitly.
func TestCplxSlashCommandSpecs_CoverExpectedSet(t *testing.T) {
	want := []string{
		"/help", "/clear", "/copy", "/model", "/session", "/context", "/branch",
		"/compact", "/subagents", "/history", "/login", "/commit", "/plan", "/run",
		"/pr-autofix",
		"/skills", "/skill-list", "/skill-load", "/skill-create", "/theme", "/ping",
		"/rtk", "/mcp", "/restart", "/exit", "/quit",
	}
	if len(slashCommandSpecs) != len(want) {
		t.Fatalf("slashCommandSpecs has %d entries, want %d", len(slashCommandSpecs), len(want))
	}
	for _, name := range want {
		spec, ok := slashCommandByName[name]
		if !ok {
			t.Errorf("command %q missing from the table", name)
			continue
		}
		if spec.desc == "" {
			t.Errorf("command %q has no description", name)
		}
		if spec.run == nil {
			t.Errorf("command %q has no handler", name)
		}
	}
}

// slashCommands feeds autocomplete, which returns the FIRST prefix match, so
// its order is behavior and not merely presentation.
func TestCplxSlashCommands_DerivedOrder(t *testing.T) {
	want := []string{
		"/help", "/clear", "/copy", "/model", "/session", "/context", "/branch",
		"/compact", "/subagents", "/history", "/login", "/commit", "/plan", "/run",
		// After /plan, so "/p" still completes to /plan and "/pr" is
		// unambiguous: autocomplete returns the first prefix match.
		"/pr-autofix",
		"/skills", "/theme", "/ping", "/rtk", "/mcp", "/restart", "/exit", "/quit",
	}
	if len(slashCommands) != len(want) {
		t.Fatalf("slashCommands = %v (%d), want %d entries", slashCommands, len(slashCommands), len(want))
	}
	for i, name := range want {
		if slashCommands[i] != name {
			t.Errorf("slashCommands[%d] = %q, want %q", i, slashCommands[i], name)
		}
	}
}

// The three skill subcommands are dispatchable and described, but deliberately
// kept out of the autocomplete list.
func TestCplxSlashCommands_HiddenAreDispatchableNotListed(t *testing.T) {
	for _, name := range []string{"/skill-list", "/skill-load", "/skill-create"} {
		if slashCommandDesc(name) == "" {
			t.Errorf("hidden command %q lost its description", name)
		}
		if _, ok := slashCommandByName[name]; !ok {
			t.Errorf("hidden command %q is not dispatchable", name)
		}
		for _, listed := range slashCommands {
			if listed == name {
				t.Errorf("hidden command %q must not appear in slashCommands", name)
			}
		}
	}
}

func TestCplxSlashCommandDesc(t *testing.T) {
	tests := []struct {
		cmd  string
		want string
	}{
		{"/help", "Show help"},
		{"/clear", "Clear conversation"},
		{"/copy", "Copy conversation to clipboard"},
		{"/rtk", "Output compaction stats"},
		{"/mcp", "List MCP servers and tool status"},
		{"/run", "Execute a spec with task agent (verifies subagent exit status before merging)"},
		{"/login", "Configure API keys (codex, openai, anthropic, gemini)"},
		{"/exit", "Exit"},
		{"/quit", "Exit"},
		// The switch's default arm: anything not a built-in has no description.
		{"/nope", ""},
		{"", ""},
		{"help", ""},
	}
	for _, tt := range tests {
		if got := slashCommandDesc(tt.cmd); got != tt.want {
			t.Errorf("slashCommandDesc(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}

func TestCplxSlashCommandAdapters(t *testing.T) {
	t.Run("args", func(t *testing.T) {
		var got []string
		run := slashCmdArgs(func(_ *model, args []string) { got = args })
		m := cplxModel(t)
		gotModel, cmd := run(m, []string{"a", "b"})
		if cmd != nil {
			t.Error("slashCmdArgs must not return a command")
		}
		if gotModel != tea.Model(m) {
			t.Error("slashCmdArgs must return the same model")
		}
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("args = %v, want [a b]", got)
		}
	})

	t.Run("bare", func(t *testing.T) {
		sentinel := tea.Cmd(func() tea.Msg { return nil })
		called := false
		run := slashCmdBare(func(m *model) (tea.Model, tea.Cmd) {
			called = true
			return m, sentinel
		})
		m := cplxModel(t)
		if _, cmd := run(m, []string{"ignored"}); cmd == nil {
			t.Error("slashCmdBare must pass the handler's command through")
		}
		if !called {
			t.Error("slashCmdBare did not call the handler")
		}
	})

	t.Run("void", func(t *testing.T) {
		called := false
		run := slashCmdVoid(func(_ *model) { called = true })
		m := cplxModel(t)
		gotModel, cmd := run(m, nil)
		if cmd != nil || gotModel != tea.Model(m) {
			t.Error("slashCmdVoid must return (m, nil)")
		}
		if !called {
			t.Error("slashCmdVoid did not call the handler")
		}
	})
}

func TestCplxHandleSlashCommand_Unknown(t *testing.T) {
	m := cplxModel(t)
	m.handleSlashCommand("/definitely-not-a-command")
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	got := m.chatModel.Messages[0].content
	if !strings.Contains(got, "Unknown command: `/definitely-not-a-command`") {
		t.Errorf("unknown-command message = %q", got)
	}
	if !strings.Contains(got, "Type `/help` for available commands.") {
		t.Errorf("unknown-command message lost its help hint: %q", got)
	}
}

// A dynamic skill shadows nothing in the table, but must still resolve through
// the same default path the switch used.
func TestCplxHandleSlashCommand_DynamicSkill(t *testing.T) {
	m := cplxModel(t)
	m.cfg.Skills = []extension.Skill{{Name: "mine", Description: "a skill"}}
	m.handleSlashCommand("/mine")
	for _, msg := range m.chatModel.Messages {
		if strings.Contains(msg.content, "Unknown command") {
			t.Fatalf("a known skill fell through to the unknown-command arm: %q", msg.content)
		}
	}
}

func TestCplxHandleSlashCommand_Quit(t *testing.T) {
	m := cplxModel(t)
	_, cmd := m.handleSlashCommand("/quit")
	if !m.quitting {
		t.Error("/quit must set quitting")
	}
	if cmd == nil {
		t.Error("/quit must return a command")
	}

	m2 := cplxModel(t)
	if _, cmd := m2.handleSlashCommand("/exit"); cmd == nil || !m2.quitting {
		t.Error("/exit must behave the same as /quit")
	}
}

// The title sync is skipped for exactly three commands. That carve-out lived in
// its own switch and must survive the table.
func TestCplxHandleSlashCommand_TitleSyncCarveOut(t *testing.T) {
	tests := []struct {
		cmd        string
		wantTitled bool
	}{
		{"/clear", false},
		{"/exit", false},
		{"/quit", false},
		{"/session", true},
		{"/help", true},
	}
	for _, tt := range tests {
		m := cplxModel(t)
		m.handleSlashCommand(tt.cmd)
		titled := m.sessionTitle != ""
		if titled != tt.wantTitled {
			t.Errorf("%s: sessionTitle set = %v, want %v (title %q)",
				tt.cmd, titled, tt.wantTitled, m.sessionTitle)
		}
	}
}

func TestCplxShowMessageHelpers(t *testing.T) {
	t.Run("session", func(t *testing.T) {
		m := cplxModel(t)
		m.showSessionMessage()
		if got := m.chatModel.Messages[0].content; got != "Session: `sess-1`" {
			t.Errorf("session message = %q", got)
		}
	})
	t.Run("help", func(t *testing.T) {
		m := cplxModel(t)
		m.showHelpMessage()
		if m.chatModel.Messages[0].content == "" {
			t.Error("help message is empty")
		}
	})
	t.Run("context", func(t *testing.T) {
		m := cplxModel(t)
		m.showContextMessage()
		if !strings.Contains(m.chatModel.Messages[0].content, "**Context Usage**") {
			t.Errorf("context message = %q", m.chatModel.Messages[0].content)
		}
	})
}

// -----------------------------------------------------------------------------
// formatContextUsage sections
// -----------------------------------------------------------------------------

func TestCplxEstimateCtxRoleTokens(t *testing.T) {
	tests := []struct {
		name string
		msgs []message
	}{
		{"empty", nil},
		{"user only", []message{{role: "user", content: strings.Repeat("a", 400)}}},
		{"assistant only", []message{{role: "assistant", content: strings.Repeat("b", 400)}}},
		// Anything that is not user or assistant lands in the tool bucket —
		// that was the switch's default arm.
		{"tool default", []message{{role: "tool", content: strings.Repeat("c", 400)}}},
		{"unknown role counts as tool", []message{{role: "weird", content: strings.Repeat("d", 400)}}},
		{"mixed", []message{
			{role: "user", content: strings.Repeat("a", 400)},
			{role: "assistant", content: strings.Repeat("b", 800)},
			{role: "tool", content: strings.Repeat("c", 1200)},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := cplxModel(t)
			m.chatModel.Messages = tt.msgs
			est := m.estimateCtxRoleTokens()

			// Recompute independently from the same primitives.
			var uc, ac, tc int
			for _, msg := range tt.msgs {
				switch msg.role {
				case "user":
					uc += messageChars(msg)
				case "assistant":
					ac += messageChars(msg)
				default:
					tc += messageChars(msg)
				}
			}
			if est.user != estimateTokens(uc) {
				t.Errorf("user = %d, want %d", est.user, estimateTokens(uc))
			}
			if est.assistant != estimateTokens(ac) {
				t.Errorf("assistant = %d, want %d", est.assistant, estimateTokens(ac))
			}
			if est.tool != estimateTokens(tc) {
				t.Errorf("tool = %d, want %d", est.tool, estimateTokens(tc))
			}
			if est.total != estimateTokens(uc+ac+tc) {
				t.Errorf("total = %d, want %d", est.total, estimateTokens(uc+ac+tc))
			}
		})
	}
}

func TestCplxWriteCtxBreakdownSection_NilIsSilent(t *testing.T) {
	m := cplxModel(t)
	m.cfg.ContextBreakdown = nil
	var b strings.Builder
	m.writeCtxBreakdownSection(&b)
	if b.String() != "" {
		t.Errorf("no breakdown configured must write nothing, got %q", b.String())
	}
}

func TestCplxWriteCtxModelLine(t *testing.T) {
	tests := []struct {
		name        string
		tracker     TokenTracker
		total       int64
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:        "with limit shows daily totals",
			tracker:     &cplxTracker{limit: 100000, totalUsed: 5000, percentUsed: 5},
			total:       1234,
			wantContain: []string{"test-provider | test-model", "/", "(5%)"},
		},
		{
			// No tracker at all: the nominal-100k arm.
			name:        "no tracker falls back to ctx estimate",
			tracker:     nil,
			total:       1234,
			wantContain: []string{"ctx ~", "test-provider | test-model"},
			wantAbsent:  []string{"tokens (0%)"},
		},
		{
			// Limit of zero is not "unlimited with a tracker" — it takes the
			// same fallback arm as a nil tracker.
			name:        "zero limit falls back too",
			tracker:     &cplxTracker{limit: 0, totalUsed: 999},
			total:       50000,
			wantContain: []string{"ctx ~"},
		},
		{
			name:        "zero total draws an empty bar",
			tracker:     nil,
			total:       0,
			wantContain: []string{"ctx ~"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := cplxModel(t)
			m.cfg.TokenTracker = tt.tracker
			var b strings.Builder
			m.writeCtxModelLine(&b, tt.total)
			got := b.String()
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("model line %q missing %q", got, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("model line %q unexpectedly contains %q", got, absent)
				}
			}
		})
	}
}

// The "never less than one block" rule and the 10k threshold above it were two
// separate branches; both still hold.
func TestCplxWriteCtxModelLine_BarFloor(t *testing.T) {
	small := cplxModel(t)
	var sb strings.Builder
	small.writeCtxModelLine(&sb, 500)

	big := cplxModel(t)
	var bb strings.Builder
	big.writeCtxModelLine(&bb, 90000)

	countFilled := func(s string) int {
		return strings.Count(s, barGlyphs(1, 1))
	}
	if countFilled(sb.String()) < 1 {
		t.Error("a short conversation must still show at least one filled block")
	}
	if countFilled(bb.String()) <= countFilled(sb.String()) {
		t.Error("90k tokens must fill more blocks than 500 tokens")
	}
}

func TestCplxWriteCtxCategorySection(t *testing.T) {
	m := cplxModel(t)
	m.chatModel.Messages = []message{
		{role: "user", content: "hi"},
		{role: "assistant", content: "hello"},
		{role: "tool", tool: "read", content: "{}"},
	}
	var b strings.Builder
	m.writeCtxCategorySection(&b, m.estimateCtxRoleTokens())
	got := b.String()
	for _, want := range []string{
		"*Estimated usage by category*",
		"- **User messages**: ~", "(1 msgs)",
		"- **Assistant messages**: ~",
		"- **Tool calls**: ~", "(1 calls)",
		"- **Total context**: ~", "(3 messages)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("category section %q missing %q", got, want)
		}
	}
}

func TestCplxWriteCtxDailySection(t *testing.T) {
	tests := []struct {
		name        string
		tracker     TokenTracker
		wantEmpty   bool
		wantContain []string
		wantAbsent  []string
	}{
		{name: "nil tracker", tracker: nil, wantEmpty: true},
		// Zero consumption suppresses the whole section, not just the numbers.
		{name: "zero used", tracker: &cplxTracker{totalUsed: 0, limit: 1000}, wantEmpty: true},
		{
			name:        "used without limit",
			tracker:     &cplxTracker{totalUsed: 4200},
			wantContain: []string{"*Daily token usage*", "**Consumed today**"},
			wantAbsent:  []string{"**Remaining**"},
		},
		{
			name:        "used with limit",
			tracker:     &cplxTracker{totalUsed: 4200, limit: 100000, remaining: 95800},
			wantContain: []string{"**Consumed today**", "**Remaining**"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := cplxModel(t)
			m.cfg.TokenTracker = tt.tracker
			var b strings.Builder
			m.writeCtxDailySection(&b)
			got := b.String()
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("expected no output, got %q", got)
				}
				return
			}
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("daily section %q missing %q", got, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("daily section %q unexpectedly contains %q", got, absent)
				}
			}
		})
	}
}

func TestCplxWriteCtxWindowSection(t *testing.T) {
	tests := []struct {
		name        string
		tracker     TokenTracker
		wantEmpty   bool
		wantContain []string
		wantAbsent  []string
	}{
		{name: "nil tracker", tracker: nil, wantEmpty: true},
		{name: "no last prompt", tracker: &cplxTracker{lastPrompt: 0, window: 200000}, wantEmpty: true},
		{
			name:    "known window",
			tracker: &cplxTracker{lastPrompt: 50000, window: 200000, ctxPercent: 25},
			wantContain: []string{
				"*Context window*", "(25%)",
				"- **Used**:", "- **Free**:",
				// The cache section always follows a rendered window section.
				"*Prompt cache*",
			},
			wantAbsent: []string{"window size unknown"},
		},
		{
			name:        "unknown window",
			tracker:     &cplxTracker{lastPrompt: 50000, window: 0},
			wantContain: []string{"- **Last prompt**:", "window size unknown", "*Prompt cache*"},
			wantAbsent:  []string{"- **Free**:"},
		},
		{
			// Free tokens clamp at zero rather than going negative when the
			// prompt overflows the advertised window.
			name:        "overflowing window clamps free at zero",
			tracker:     &cplxTracker{lastPrompt: 300000, window: 200000, ctxPercent: 150},
			wantContain: []string{"- **Free**: 0 tokens"},
			wantAbsent:  []string{"-"[0:0] + "**Free**: -"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := cplxModel(t)
			m.cfg.TokenTracker = tt.tracker
			var b strings.Builder
			m.writeCtxWindowSection(&b)
			got := b.String()
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("expected no output, got %q", got)
				}
				return
			}
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("window section %q missing %q", got, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if absent != "" && strings.Contains(got, absent) {
					t.Errorf("window section %q unexpectedly contains %q", got, absent)
				}
			}
		})
	}
}

func TestCplxWriteCtxCacheSection(t *testing.T) {
	tests := []struct {
		name        string
		tracker     *cplxTracker
		wantContain []string
		wantAbsent  []string
	}{
		{
			// Reported explicitly at zero hits — a silent section would read the
			// same whether caching works or is entirely absent.
			name:        "no cache hit",
			tracker:     &cplxTracker{},
			wantContain: []string{"*Prompt cache*", "- **Last request**: no cache hit"},
			wantAbsent:  []string{"**Today**", "**Stable prefix**"},
		},
		{
			name:        "cache hit",
			tracker:     &cplxTracker{lastCached: 5000},
			wantContain: []string{"prompt tokens cached (50%)"},
		},
		{
			name:        "today only",
			tracker:     &cplxTracker{cachedToday: 12345, hitRate: 42},
			wantContain: []string{"**Today**", "(42% of input)"},
			wantAbsent:  []string{"**Stable prefix**"},
		},
		{
			name:        "prefix and body",
			tracker:     &cplxTracker{prefix: 8000, body: 2000},
			wantContain: []string{"**Stable prefix**", "**body since**"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := cplxModel(t)
			m.cfg.TokenTracker = tt.tracker
			var b strings.Builder
			m.writeCtxCacheSection(&b, 10000)
			got := b.String()
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("cache section %q missing %q", got, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("cache section %q unexpectedly contains %q", got, absent)
				}
			}
		})
	}
}

func TestCplxWriteCtxSkillsSection(t *testing.T) {
	t.Run("no skills is silent", func(t *testing.T) {
		m := cplxModel(t)
		var b strings.Builder
		m.writeCtxSkillsSection(&b)
		if b.String() != "" {
			t.Errorf("expected no output, got %q", b.String())
		}
	})

	t.Run("alphabetical with defaults", func(t *testing.T) {
		m := cplxModel(t)
		m.cfg.Skills = []extension.Skill{
			{Name: "zebra", Description: "last", Source: "project"},
			// Empty description and source take the defaulted values.
			{Name: "alpha"},
		}
		var b strings.Builder
		m.writeCtxSkillsSection(&b)
		got := b.String()
		if !strings.Contains(got, "(2 loaded)") {
			t.Errorf("skills section %q missing the count", got)
		}
		ai, zi := strings.Index(got, "/alpha"), strings.Index(got, "/zebra")
		if ai < 0 || zi < 0 || ai > zi {
			t.Errorf("skills must list alphabetically, got %q", got)
		}
		if !strings.Contains(got, "(no description)") {
			t.Errorf("a skill with no description must say so, got %q", got)
		}
		if !strings.Contains(got, "[user]") {
			t.Errorf("a skill with no source must default to user, got %q", got)
		}
		if !strings.Contains(got, "[project]") {
			t.Errorf("an explicit source must survive, got %q", got)
		}
		if !strings.Contains(got, "body: not loaded") {
			t.Errorf("an unloaded body must say so, got %q", got)
		}
	})
}

func TestCplxWriteCtxSubagentSection_NilOrchestrator(t *testing.T) {
	m := cplxModel(t)
	m.cfg.Orchestrator = nil
	var b strings.Builder
	m.writeCtxSubagentSection(&b)
	if b.String() != "" {
		t.Errorf("no orchestrator must write nothing, got %q", b.String())
	}
}

// The composed output must still arrive in the original section order.
func TestCplxFormatContextUsage_SectionOrder(t *testing.T) {
	m := cplxModel(t)
	m.cfg.TokenTracker = &cplxTracker{
		limit: 100000, totalUsed: 5000, percentUsed: 5,
		remaining: 95000, lastPrompt: 40000, window: 200000, ctxPercent: 20,
	}
	m.cfg.Skills = []extension.Skill{{Name: "s1", Description: "d"}}
	m.chatModel.Messages = []message{{role: "user", content: "hi"}}

	out := m.formatContextUsage()
	order := []string{
		"**Context Usage**",
		"*Estimated usage by category*",
		"*Daily token usage*",
		"*Context window*",
		"*Prompt cache*",
		"*Skills*",
	}
	prev := -1
	for _, section := range order {
		at := strings.Index(out, section)
		if at < 0 {
			t.Fatalf("output missing section %q:\n%s", section, out)
		}
		if at < prev {
			t.Errorf("section %q is out of order", section)
		}
		prev = at
	}
}

// -----------------------------------------------------------------------------
// run.go — /run argument parsing, summary report, agent events
// -----------------------------------------------------------------------------

func TestCplxParseRunArgs(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		wantSpec     string
		wantParallel bool
		wantForce    bool
	}{
		{"empty", nil, "", false, false},
		{"spec only", []string{"my-spec"}, "my-spec", false, false},
		{"long flag", []string{"my-spec", "--parallel"}, "my-spec", true, false},
		{"short flag", []string{"my-spec", "-p"}, "my-spec", true, false},
		{"flag first", []string{"-p", "my-spec"}, "my-spec", true, false},
		// The first non-flag argument wins; later ones are ignored.
		{"extra positional ignored", []string{"first", "second"}, "first", false, false},
		{"flag only", []string{"--parallel"}, "", true, false},
		{"repeated flags", []string{"-p", "--parallel", "spec"}, "spec", true, false},
		{"force long", []string{"my-spec", "--force"}, "my-spec", false, true},
		{"force short", []string{"my-spec", "-f"}, "my-spec", false, true},
		{"force and parallel", []string{"-f", "-p", "my-spec"}, "my-spec", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, parallel, force := parseRunArgs(tt.args)
			if spec != tt.wantSpec || parallel != tt.wantParallel || force != tt.wantForce {
				t.Errorf("parseRunArgs(%v) = (%q, %v, %v), want (%q, %v, %v)",
					tt.args, spec, parallel, force, tt.wantSpec, tt.wantParallel, tt.wantForce)
			}
		})
	}
}

func TestCplxFormatRunGateInfo(t *testing.T) {
	tests := []struct {
		name  string
		gates []Gate
		want  string
	}{
		{"none", nil, "none"},
		{"empty slice", []Gate{}, "none"},
		{"one", []Gate{{Name: "build"}}, "build"},
		{"many", []Gate{{Name: "build"}, {Name: "test"}, {Name: "lint"}}, "build, test, lint"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatRunGateInfo(tt.gates); got != tt.want {
				t.Errorf("formatRunGateInfo = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCplxHandleRunCommand_NoArgsShowsUsage(t *testing.T) {
	m := cplxModel(t)
	m.handleRunCommand(nil)
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.chatModel.Messages))
	}
	if !strings.Contains(m.chatModel.Messages[0].content, "Usage: `/run <spec-name> [--parallel] [--force]`") {
		t.Errorf("usage message = %q", m.chatModel.Messages[0].content)
	}
}

func TestCplxHandleRunCommand_NoOrchestrator(t *testing.T) {
	m := cplxModel(t)
	m.cfg.Orchestrator = nil
	m.handleRunCommand([]string{"some-spec"})
	if got := m.chatModel.Messages[0].content; got != "Subagent system not available. Cannot run specs." {
		t.Errorf("message = %q", got)
	}
}

// A bare --parallel gives no spec name, and the missing-name arm must fire
// before anything tries to read a spec off disk.
func TestCplxHandleRunCommand_MissingSpecName(t *testing.T) {
	m := cplxModel(t)
	m.cfg.Orchestrator = &subagent.Orchestrator{}
	m.handleRunCommand([]string{"--parallel"})
	if got := m.chatModel.Messages[0].content; got != "Missing spec name." {
		t.Errorf("message = %q", got)
	}
}

func TestCplxShowRunSpecError(t *testing.T) {
	m := cplxModel(t)
	m.showRunSpecError(context.DeadlineExceeded)
	if got := m.chatModel.Messages[0].content; !strings.HasPrefix(got, "Error: ") {
		t.Errorf("spec error message = %q, want an Error: prefix", got)
	}
}

func TestCplxWriteRunSummaryMetadata(t *testing.T) {
	start := time.Now().Add(-90 * time.Second)
	tests := []struct {
		name        string
		rs          *runState
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:        "minimal",
			rs:          &runState{specName: "s", agentID: "a", retries: 1, maxRetries: 10},
			wantContain: []string{"| Spec | `s` |", "| Agent | `a` |", "| Retries | 1 / 10 |"},
			wantAbsent:  []string{"| Slices |", "| Started |", "| Duration |"},
		},
		{
			name: "with checklist and start time",
			rs: &runState{
				specName: "s", agentID: "a", maxRetries: 10, startTime: start,
				checklist: []ChecklistStep{{Title: "one", Done: true}, {Title: "two"}},
			},
			wantContain: []string{"| Slices | 1 / 2 complete |", "| Started |", "| Duration |"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			writeRunSummaryMetadata(&b, tt.rs, "completed")
			got := b.String()
			if !strings.Contains(got, "| Outcome | **completed** |") {
				t.Errorf("metadata %q missing the outcome row", got)
			}
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("metadata %q missing %q", got, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("metadata %q unexpectedly contains %q", got, absent)
				}
			}
		})
	}
}

func TestCplxWriteRunSummaryGates(t *testing.T) {
	tests := []struct {
		name        string
		rs          *runState
		wantContain []string
		wantAbsent  []string
	}{
		{
			name:        "none defined",
			rs:          &runState{},
			wantContain: []string{"## Gates", "No gates defined."},
		},
		{
			// Defined but never executed is its own arm, distinct from "none".
			name:        "defined not executed",
			rs:          &runState{gates: []Gate{{Name: "build", Command: "make build"}}},
			wantContain: []string{"Gates were defined but not executed.", "- **build**: `make build`"},
			wantAbsent:  []string{"No gates defined."},
		},
		{
			name: "all passed",
			rs: &runState{gateResults: []GateResult{
				{Name: "build", Command: "make build", Passed: true},
				{Name: "test", Command: "make test", Passed: true},
			}},
			wantContain: []string{"**PASS**", "All gates **passed**."},
			wantAbsent:  []string{"Some gates **failed**.", "FAIL"},
		},
		{
			name: "one failed",
			rs: &runState{gateResults: []GateResult{
				{Name: "build", Command: "make build", Passed: true},
				{Name: "test", Command: "make test", Passed: false, Output: "  boom  "},
			}},
			wantContain: []string{"**FAIL**", "boom", "Some gates **failed**."},
			wantAbsent:  []string{"All gates **passed**."},
		},
		{
			// A failure with no captured output prints the verdict but no block.
			name: "failed without output",
			rs: &runState{gateResults: []GateResult{
				{Name: "test", Command: "make test", Passed: false},
			}},
			wantContain: []string{"**FAIL**", "Some gates **failed**."},
			wantAbsent:  []string{"```"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			writeRunSummaryGates(&b, tt.rs)
			got := b.String()
			for _, want := range tt.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("gates section %q missing %q", got, want)
				}
			}
			for _, absent := range tt.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("gates section %q unexpectedly contains %q", got, absent)
				}
			}
		})
	}
}

// Output over 1000 bytes is truncated with a marker. That boundary is behavior.
func TestCplxWriteRunSummaryGateResult_TruncatesLongOutput(t *testing.T) {
	var b strings.Builder
	writeRunSummaryGateResult(&b, GateResult{
		Name: "test", Command: "make test", Passed: false,
		Output: strings.Repeat("x", 1500),
	})
	got := b.String()
	if !strings.Contains(got, "...(truncated)") {
		t.Error("output over 1000 bytes must be truncated")
	}
	if strings.Count(got, "x") != 1000 {
		t.Errorf("expected exactly 1000 retained bytes, got %d", strings.Count(got, "x"))
	}

	var short strings.Builder
	writeRunSummaryGateResult(&short, GateResult{
		Name: "test", Command: "make test", Passed: false,
		Output: strings.Repeat("y", 1000),
	})
	if strings.Contains(short.String(), "...(truncated)") {
		t.Error("output of exactly 1000 bytes must not be truncated")
	}
}

func TestCplxWriteRunSummaryResult(t *testing.T) {
	tests := []struct {
		outcome string
		want    string
	}{
		{"completed", "All gates passed, the plan checklist was complete, and changes were merged successfully."},
		{"gate_failed", "Gate validation failed after 3 retries."},
		{"verify_failed", "Gates passed but the plan was still incomplete after 3 retries"},
		{"merge_failed", "Gates passed but merge into the main branch failed."},
		{"agent_failed", "Subagent process exited with non-zero status"},
		// The default arm echoes whatever status it was handed.
		{"something_else", "Run ended with status: something_else"},
		{"", "Run ended with status: "},
	}
	for _, tt := range tests {
		t.Run(tt.outcome, func(t *testing.T) {
			var b strings.Builder
			writeRunSummaryResult(&b, &runState{retries: 3}, tt.outcome)
			got := b.String()
			if !strings.HasPrefix(got, "## Result\n\n") {
				t.Errorf("result section %q missing its heading", got)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("result for %q = %q, want it to contain %q", tt.outcome, got, tt.want)
			}
		})
	}
}

func TestCplxBuildRunSummaryReport_Composition(t *testing.T) {
	rs := &runState{
		specName: "spec-1", agentID: "agent-1", retries: 2, maxRetries: 10,
		checklist:   []ChecklistStep{{Title: "done one", Done: true}, {Title: "pending two"}},
		gateResults: []GateResult{{Name: "build", Command: "make build", Passed: true}},
	}
	out := buildRunSummaryReport(rs, "completed")
	for _, want := range []string{
		"# Run Summary", "## Metadata", "## Gates",
		"## Unfinished Slices", "pending two", "## Result",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	// Section order must be stable.
	if strings.Index(out, "## Metadata") > strings.Index(out, "## Gates") {
		t.Error("metadata must precede gates")
	}
	if strings.Index(out, "## Unfinished Slices") > strings.Index(out, "## Result") {
		t.Error("unfinished slices must precede the result")
	}
}

// A run with every slice ticked has no Unfinished Slices section at all.
func TestCplxBuildRunSummaryReport_NoUnfinishedSection(t *testing.T) {
	rs := &runState{
		specName:  "spec-1",
		checklist: []ChecklistStep{{Title: "one", Done: true}},
	}
	if out := buildRunSummaryReport(rs, "completed"); strings.Contains(out, "## Unfinished Slices") {
		t.Errorf("a fully ticked checklist must not produce an unfinished section:\n%s", out)
	}
}

func TestCplxApplyRunTextDelta(t *testing.T) {
	t.Run("fills the open assistant message", func(t *testing.T) {
		m := cplxModel(t)
		m.chatModel.Messages = []message{
			{role: "assistant", content: "stale"},
			{role: "tool", content: "t"},
		}
		m.chatModel.Scroll = 7
		m.applyRunTextDelta(subagent.Event{Type: "text_delta", Content: "hel"})
		m.applyRunTextDelta(subagent.Event{Type: "text_delta", Content: "lo"})

		if m.chatModel.Streaming != "hello" {
			t.Errorf("Streaming = %q, want %q", m.chatModel.Streaming, "hello")
		}
		if m.chatModel.Messages[0].content != "hello" {
			t.Errorf("assistant message = %q, want %q", m.chatModel.Messages[0].content, "hello")
		}
		if m.chatModel.Scroll != 0 {
			t.Errorf("Scroll = %d, want 0 (a delta jumps to the bottom)", m.chatModel.Scroll)
		}
	})

	t.Run("folds into a trailing llm trace entry", func(t *testing.T) {
		m := cplxModel(t)
		m.chatModel.Messages = []message{{role: "assistant"}}
		m.applyRunTextDelta(subagent.Event{Content: "a"})
		m.applyRunTextDelta(subagent.Event{Content: "b"})
		if len(m.chatModel.TraceLog) != 1 {
			t.Fatalf("expected the second delta to fold into one trace entry, got %d",
				len(m.chatModel.TraceLog))
		}
		if got := m.chatModel.TraceLog[0].detail; got != "ab" {
			t.Errorf("trace detail = %q, want %q", got, "ab")
		}
	})

	t.Run("opens a new trace entry after a tool call", func(t *testing.T) {
		m := cplxModel(t)
		m.chatModel.Messages = []message{{role: "assistant"}}
		m.chatModel.TraceLog = []traceEntry{{kind: "tool_call"}}
		m.applyRunTextDelta(subagent.Event{Content: "a"})
		if len(m.chatModel.TraceLog) != 2 {
			t.Fatalf("expected a new llm trace entry, got %d", len(m.chatModel.TraceLog))
		}
		if m.chatModel.TraceLog[1].kind != "llm" {
			t.Errorf("new trace entry kind = %q, want llm", m.chatModel.TraceLog[1].kind)
		}
	})
}

func TestCplxApplyRunToolCall(t *testing.T) {
	t.Run("with args", func(t *testing.T) {
		m := cplxModel(t)
		m.applyRunToolCall(subagent.Event{
			Type: "tool_call", Content: "read",
			ToolArgs: map[string]any{"file_path": "/tmp/x.go"},
		})
		if m.statusModel.ActiveTool != "read" {
			t.Errorf("ActiveTool = %q, want read", m.statusModel.ActiveTool)
		}
		last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
		if last.role != "tool" || last.tool != "read" {
			t.Errorf("expected a tool message for read, got %+v", last)
		}
		if last.toolIn == "" {
			t.Error("tool args must produce a toolIn one-liner")
		}
	})

	t.Run("without args", func(t *testing.T) {
		m := cplxModel(t)
		// A non-map ToolArgs takes the other arm and leaves toolIn empty.
		m.applyRunToolCall(subagent.Event{Content: "read", ToolArgs: "not-a-map"})
		last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
		if last.toolIn != "" {
			t.Errorf("toolIn = %q, want empty when args are not a map", last.toolIn)
		}
		if len(m.chatModel.TraceLog) != 1 || m.chatModel.TraceLog[0].kind != "tool_call" {
			t.Errorf("expected one tool_call trace entry, got %+v", m.chatModel.TraceLog)
		}
	})
}

func TestCplxApplyRunToolResult(t *testing.T) {
	t.Run("fills the open tool message", func(t *testing.T) {
		m := cplxModel(t)
		m.statusModel.ActiveTool = "read"
		m.chatModel.Messages = []message{
			{role: "tool", tool: "read", content: "already done"},
			{role: "tool", tool: "read", content: ""},
		}
		m.applyRunToolResult(subagent.Event{Type: "tool_result", Content: "ok"})

		if m.statusModel.ActiveTool != "" {
			t.Errorf("ActiveTool = %q, want cleared", m.statusModel.ActiveTool)
		}
		if m.chatModel.Messages[0].content != "already done" {
			t.Error("a completed tool message must not be overwritten")
		}
		if m.chatModel.Messages[1].content == "" {
			t.Error("the open tool message must receive the result")
		}
	})

	t.Run("no open tool message", func(t *testing.T) {
		m := cplxModel(t)
		m.chatModel.Messages = []message{{role: "assistant", content: "x"}}
		m.applyRunToolResult(subagent.Event{Content: "ok"})
		if m.chatModel.Messages[0].content != "x" {
			t.Error("with nothing open, no message may change")
		}
	})

	t.Run("nil run skips the checklist refresh", func(t *testing.T) {
		m := cplxModel(t)
		m.run = nil
		m.statusModel.ActiveTool = "write"
		// Must not panic: the refresh is guarded on m.run.
		m.applyRunToolResult(subagent.Event{Content: "ok"})
	})
}

func TestCplxApplyRunError(t *testing.T) {
	m := cplxModel(t)
	m.applyRunError(subagent.Event{Type: "error", Error: "it broke"})
	if got := m.chatModel.Messages[0].content; got != "Agent error: it broke" {
		t.Errorf("message = %q", got)
	}
	if len(m.chatModel.TraceLog) != 1 || m.chatModel.TraceLog[0].kind != "error" {
		t.Errorf("expected one error trace entry, got %+v", m.chatModel.TraceLog)
	}
}

// handleRunAgentEvent still routes every event type, and always keeps consuming.
func TestCplxHandleRunAgentEvent_Routing(t *testing.T) {
	tests := []struct {
		name  string
		event subagent.Event
		check func(t *testing.T, m *model)
	}{
		{
			name:  "text_delta",
			event: subagent.Event{Type: "text_delta", Content: "hi"},
			check: func(t *testing.T, m *model) {
				if m.chatModel.Streaming != "hi" {
					t.Errorf("Streaming = %q", m.chatModel.Streaming)
				}
			},
		},
		{
			name:  "tool_call",
			event: subagent.Event{Type: "tool_call", Content: "read"},
			check: func(t *testing.T, m *model) {
				if m.statusModel.ActiveTool != "read" {
					t.Errorf("ActiveTool = %q", m.statusModel.ActiveTool)
				}
			},
		},
		{
			name:  "tool_result",
			event: subagent.Event{Type: "tool_result", Content: "ok"},
			check: func(t *testing.T, m *model) {
				if m.statusModel.ActiveTool != "" {
					t.Errorf("ActiveTool = %q, want cleared", m.statusModel.ActiveTool)
				}
			},
		},
		{
			name:  "message_start",
			event: subagent.Event{Type: "message_start"},
			check: func(t *testing.T, m *model) {
				if m.chatModel.Streaming != "" {
					t.Error("message_start must reset the accumulator")
				}
				last := m.chatModel.Messages[len(m.chatModel.Messages)-1]
				if last.role != "assistant" || last.content != "" {
					t.Errorf("expected an empty assistant placeholder, got %+v", last)
				}
			},
		},
		{
			name:  "message_end",
			event: subagent.Event{Type: "message_end"},
			check: func(t *testing.T, m *model) {
				if m.chatModel.Streaming != "" {
					t.Error("message_end must reset the accumulator")
				}
			},
		},
		{
			name:  "error",
			event: subagent.Event{Type: "error", Error: "boom"},
			check: func(t *testing.T, m *model) {
				if !strings.Contains(m.chatModel.Messages[0].content, "boom") {
					t.Errorf("messages = %+v", m.chatModel.Messages)
				}
			},
		},
		{
			// An unrecognized type falls through untouched but still keeps the
			// event pump alive.
			name:  "unknown type",
			event: subagent.Event{Type: "never_heard_of_it"},
			check: func(t *testing.T, m *model) {
				if len(m.chatModel.Messages) != 0 {
					t.Errorf("an unknown event must change nothing, got %+v", m.chatModel.Messages)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := cplxModel(t)
			// A live run with an open channel, so the "keep consuming" cmd is
			// actually produced rather than short-circuited by a nil run.
			events := make(chan subagent.Event, 1)
			m.run = &runState{agentID: "a1", events: events, phase: "running"}
			m.chatModel.Streaming = "leftover"
			if tt.event.Type == "text_delta" {
				m.chatModel.Streaming = ""
			}
			_, cmd := m.handleRunAgentEvent(runAgentEventMsg{event: tt.event, agentID: "a1"})
			if cmd == nil {
				t.Error("handleRunAgentEvent must keep consuming events")
			}
			tt.check(t, m)
		})
	}
}

// -----------------------------------------------------------------------------
// tui.go — search popup keys, styles, viewport clipping, terminal dispatch
// -----------------------------------------------------------------------------

func cplxPopup(mode searchMode, n, height int) *searchPopupState {
	items := make([]SearchItem, n)
	for i := range items {
		items[i] = SearchItem{Text: string(rune('a' + i)), Description: "desc"}
	}
	return &searchPopupState{mode: mode, entries: items, filtered: items, height: height}
}

func TestCplxSearchPopupSelectPrev(t *testing.T) {
	tests := []struct {
		name          string
		items         int
		selected      int
		height        int
		wantSelected  int
		wantScrollOff int
	}{
		{"moves up", 5, 3, 3, 2, 0},
		{"wraps from first", 5, 0, 3, 4, 2},
		// A single item has nowhere to wrap to.
		{"single item stays", 1, 0, 3, 0, 0},
		{"empty stays", 0, 0, 3, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := cplxPopup(searchModeCommands, tt.items, tt.height)
			sp.selected = tt.selected
			sp.selectPrev()
			if sp.selected != tt.wantSelected {
				t.Errorf("selected = %d, want %d", sp.selected, tt.wantSelected)
			}
			if sp.scrollOff != tt.wantScrollOff {
				t.Errorf("scrollOff = %d, want %d", sp.scrollOff, tt.wantScrollOff)
			}
		})
	}
}

func TestCplxSearchPopupSelectNext(t *testing.T) {
	tests := []struct {
		name          string
		items         int
		selected      int
		scrollOff     int
		height        int
		wantSelected  int
		wantScrollOff int
	}{
		{"moves down", 5, 1, 0, 3, 2, 0},
		// Advancing past the visible window scrolls it.
		{"scrolls when past the window", 5, 2, 0, 3, 3, 1},
		{"wraps from last", 5, 4, 2, 3, 0, 2},
		{"single item stays", 1, 0, 0, 3, 0, 0},
		{"empty stays", 0, 0, 0, 3, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := cplxPopup(searchModeCommands, tt.items, tt.height)
			sp.selected, sp.scrollOff = tt.selected, tt.scrollOff
			sp.selectNext()
			if sp.selected != tt.wantSelected {
				t.Errorf("selected = %d, want %d", sp.selected, tt.wantSelected)
			}
			if sp.scrollOff != tt.wantScrollOff {
				t.Errorf("scrollOff = %d, want %d", sp.scrollOff, tt.wantScrollOff)
			}
		})
	}
}

func TestCplxSearchPopupSelectByTab(t *testing.T) {
	tests := []struct {
		name         string
		items        int
		selected     int
		backwards    bool
		wantSelected int
	}{
		{"tab advances", 5, 1, false, 2},
		// Tab stops at the last item — unlike Down, it does not wrap.
		{"tab stops at last", 5, 4, false, 4},
		{"shift-tab retreats", 5, 3, true, 2},
		{"shift-tab wraps from first", 5, 0, true, 4},
		{"empty is a no-op", 0, 0, false, 0},
		{"empty backwards is a no-op", 0, 0, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := cplxPopup(searchModeCommands, tt.items, 3)
			sp.selected = tt.selected
			sp.selectByTab(tt.backwards)
			if sp.selected != tt.wantSelected {
				t.Errorf("selected = %d, want %d", sp.selected, tt.wantSelected)
			}
		})
	}
}

func TestCplxAcceptSearchPopupSelection(t *testing.T) {
	t.Run("command gets a trailing space", func(t *testing.T) {
		m := cplxModel(t)
		m.searchPopup = cplxPopup(searchModeCommands, 3, 3)
		m.searchPopup.selected = 1
		m.acceptSearchPopupSelection()
		if got := m.inputModel.Text; got != "b " {
			t.Errorf("input = %q, want %q", got, "b ")
		}
		if m.searchPopup != nil {
			t.Error("accepting must close the popup")
		}
	})

	t.Run("history is inserted verbatim", func(t *testing.T) {
		m := cplxModel(t)
		m.searchPopup = cplxPopup(searchModeHistory, 3, 3)
		m.searchPopup.selected = 2
		m.acceptSearchPopupSelection()
		if got := m.inputModel.Text; got != "c" {
			t.Errorf("input = %q, want %q", got, "c")
		}
		if m.searchPopup != nil {
			t.Error("accepting must close the popup")
		}
	})

	t.Run("empty list leaves the popup open", func(t *testing.T) {
		m := cplxModel(t)
		m.searchPopup = cplxPopup(searchModeCommands, 0, 3)
		m.acceptSearchPopupSelection()
		if m.searchPopup == nil {
			t.Error("nothing to accept must leave the popup open")
		}
		if m.inputModel.Text != "" {
			t.Errorf("input = %q, want empty", m.inputModel.Text)
		}
	})

	t.Run("out of range selection is ignored", func(t *testing.T) {
		m := cplxModel(t)
		m.searchPopup = cplxPopup(searchModeCommands, 2, 3)
		m.searchPopup.selected = 9
		m.acceptSearchPopupSelection()
		if m.searchPopup == nil || m.inputModel.Text != "" {
			t.Error("an out-of-range selection must change nothing")
		}
	})
}

func TestCplxHandleSearchPopupKey(t *testing.T) {
	t.Run("nil popup is not handled", func(t *testing.T) {
		m := cplxModel(t)
		if m.handleSearchPopupKey(tea.Key{Code: tea.KeyUp}) {
			t.Error("with no popup the key must not be consumed")
		}
	})

	t.Run("esc closes", func(t *testing.T) {
		m := cplxModel(t)
		m.searchPopup = cplxPopup(searchModeCommands, 3, 3)
		if !m.handleSearchPopupKey(tea.Key{Code: tea.KeyEsc}) {
			t.Error("esc must be consumed")
		}
		if m.searchPopup != nil {
			t.Error("esc must close the popup")
		}
	})

	t.Run("backspace trims then closes", func(t *testing.T) {
		m := cplxModel(t)
		m.searchPopup = cplxPopup(searchModeCommands, 3, 3)
		m.searchPopup.search = "ab"
		m.handleSearchPopupKey(tea.Key{Code: tea.KeyBackspace})
		if m.searchPopup == nil || m.searchPopup.search != "a" {
			t.Fatalf("backspace must trim the query, got %+v", m.searchPopup)
		}
		m.handleSearchPopupKey(tea.Key{Code: tea.KeyBackspace})
		if m.searchPopup == nil || m.searchPopup.search != "" {
			t.Fatal("backspace must empty the query before closing")
		}
		m.handleSearchPopupKey(tea.Key{Code: tea.KeyBackspace})
		if m.searchPopup != nil {
			t.Error("backspace on an empty query must close the popup")
		}
	})

	t.Run("printable runes filter", func(t *testing.T) {
		m := cplxModel(t)
		m.searchPopup = cplxPopup(searchModeCommands, 3, 3)
		if !m.handleSearchPopupKey(tea.Key{Code: 'x', Text: "x"}) {
			t.Error("a printable rune must be consumed")
		}
		if m.searchPopup.search != "x" {
			t.Errorf("search = %q, want x", m.searchPopup.search)
		}
	})

	t.Run("modified and multi-rune keys fall through", func(t *testing.T) {
		m := cplxModel(t)
		m.searchPopup = cplxPopup(searchModeCommands, 3, 3)
		if m.handleSearchPopupKey(tea.Key{Code: 'x', Text: "x", Mod: tea.ModCtrl}) {
			t.Error("ctrl+x must not be swallowed as search text")
		}
		if m.handleSearchPopupKey(tea.Key{Code: 'x', Text: "xy"}) {
			t.Error("multi-rune text must not be swallowed as search text")
		}
		if m.handleSearchPopupKey(tea.Key{Code: tea.KeyF1}) {
			t.Error("an unhandled key must not be consumed")
		}
	})

	t.Run("navigation keys are consumed", func(t *testing.T) {
		for _, key := range []tea.Key{
			{Code: tea.KeyUp}, {Code: tea.KeyDown},
			{Code: tea.KeyTab}, {Code: tea.KeyTab, Mod: tea.ModShift},
			{Code: tea.KeyEnter},
		} {
			m := cplxModel(t)
			m.searchPopup = cplxPopup(searchModeCommands, 3, 3)
			if !m.handleSearchPopupKey(key) {
				t.Errorf("key %v must be consumed", key.Code)
			}
		}
	})
}

func TestCplxSearchPopupStyles(t *testing.T) {
	m := cplxModel(t)
	commands := m.searchPopupStyles(searchModeCommands, 40)
	if commands.header != "Commands" {
		t.Errorf("commands header = %q", commands.header)
	}
	history := m.searchPopupStyles(searchModeHistory, 40)
	if history.header != "History" {
		t.Errorf("history header = %q", history.header)
	}
	// The two modes must differ, or the accent coloring was lost in the move.
	if commands.itemStyle.Render("x") == history.itemStyle.Render("x") {
		t.Error("commands and history must render items differently")
	}
	// An unknown mode keeps the unaccented base styles and an empty header.
	unknown := m.searchPopupStyles(searchMode("nope"), 40)
	if unknown.header != "" {
		t.Errorf("unknown mode header = %q, want empty", unknown.header)
	}
}

func TestCplxRenderSearchPopup(t *testing.T) {
	t.Run("nil popup renders nothing", func(t *testing.T) {
		m := cplxModel(t)
		if got := m.renderSearchPopup(40); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("empty results per mode", func(t *testing.T) {
		for mode, want := range map[searchMode]string{
			searchModeCommands: "No matching commands",
			searchModeHistory:  "No matching history",
		} {
			m := cplxModel(t)
			m.searchPopup = cplxPopup(mode, 0, 3)
			if got := m.renderSearchPopup(40); !strings.Contains(got, want) {
				t.Errorf("mode %q: output missing %q", mode, want)
			}
		}
	})

	t.Run("search prompt line", func(t *testing.T) {
		m := cplxModel(t)
		m.searchPopup = cplxPopup(searchModeCommands, 3, 3)
		if got := m.renderSearchPopup(40); !strings.Contains(got, "Search... (type to filter)") {
			t.Errorf("an empty query must show the placeholder, got %q", got)
		}
		m.searchPopup.search = "abc"
		if got := m.renderSearchPopup(40); !strings.Contains(got, "Search: abc") {
			t.Errorf("a query must be echoed, got %q", got)
		}
	})

	t.Run("header carries the count", func(t *testing.T) {
		m := cplxModel(t)
		m.searchPopup = cplxPopup(searchModeCommands, 3, 3)
		if got := m.renderSearchPopup(40); !strings.Contains(got, "Commands (3)") {
			t.Errorf("header missing its count, got %q", got)
		}
	})

	// A width under the floor is widened rather than producing a broken frame.
	t.Run("narrow width does not panic", func(t *testing.T) {
		m := cplxModel(t)
		m.searchPopup = cplxPopup(searchModeCommands, 3, 3)
		if got := m.renderSearchPopup(2); got == "" {
			t.Error("a narrow popup must still render")
		}
	})
}

func TestCplxWriteSearchPopupItems(t *testing.T) {
	t.Run("marks the selection and stops at the end", func(t *testing.T) {
		m := cplxModel(t)
		sp := cplxPopup(searchModeCommands, 2, 5) // height exceeds the item count
		sp.selected = 1
		var b strings.Builder
		writeSearchPopupItems(&b, sp, m.searchPopupStyles(searchModeCommands, 40), 40)
		got := b.String()
		if strings.Count(got, "\n") != 2 {
			t.Errorf("expected exactly 2 item rows, got %q", got)
		}
		if !strings.Contains(got, "> ") {
			t.Error("the selected row must carry the > marker")
		}
	})

	t.Run("a stale scroll offset cannot index past the end", func(t *testing.T) {
		m := cplxModel(t)
		sp := cplxPopup(searchModeCommands, 2, 5)
		sp.scrollOff = 99
		var b strings.Builder
		writeSearchPopupItems(&b, sp, m.searchPopupStyles(searchModeCommands, 40), 40)
		if b.String() != "" {
			t.Errorf("an out-of-range offset must render nothing, got %q", b.String())
		}
	})

	t.Run("descriptions only in command mode", func(t *testing.T) {
		m := cplxModel(t)
		var withDesc, noDesc strings.Builder
		writeSearchPopupItems(&withDesc, cplxPopup(searchModeCommands, 1, 3),
			m.searchPopupStyles(searchModeCommands, 60), 60)
		writeSearchPopupItems(&noDesc, cplxPopup(searchModeHistory, 1, 3),
			m.searchPopupStyles(searchModeHistory, 60), 60)
		if !strings.Contains(withDesc.String(), "desc") {
			t.Error("command rows must carry their description")
		}
		if strings.Contains(noDesc.String(), "desc") {
			t.Error("history rows must not carry a description")
		}
	})
}

func TestCplxClipMessagesToViewport(t *testing.T) {
	tests := []struct {
		name          string
		view          string
		height        int
		scroll        int
		wantStart     int
		wantEnd       int
		wantRowsAtMin int
	}{
		// Shorter than the viewport: clamped to 0 and padded out to height.
		{"pads a short block", "a\nb", 5, 0, 0, 2, 5},
		{"exact fit", "a\nb\nc", 3, 0, 0, 3, 3},
		{"trims the top", "a\nb\nc\nd\ne", 3, 0, 2, 5, 3},
		{"scroll moves the window back", "a\nb\nc\nd\ne", 3, 2, 0, 3, 3},
		// Scrolling past the start clamps rather than going negative.
		{"scroll past the start clamps", "a\nb\nc", 2, 99, 0, 2, 2},
		{"single line", "only", 1, 0, 0, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			visible, start, end := clipMessagesToViewport(tt.view, tt.height, tt.scroll)
			if start != tt.wantStart || end != tt.wantEnd {
				t.Errorf("range = [%d,%d), want [%d,%d)", start, end, tt.wantStart, tt.wantEnd)
			}
			if rows := strings.Count(visible, "\n") + 1; rows != tt.wantRowsAtMin {
				t.Errorf("rows = %d, want %d (visible %q)", rows, tt.wantRowsAtMin, visible)
			}
		})
	}
}

// Padding rows must stay materialized — a bare trailing newline made the
// composed frame one line shorter than the terminal.
func TestCplxClipMessagesToViewport_PaddingRowsAreMaterialized(t *testing.T) {
	visible, _, _ := clipMessagesToViewport("a", 4, 0)
	if !strings.HasSuffix(visible, "\n ") {
		t.Errorf("padded block must end in a materialized row, got %q", visible)
	}
}

func TestCplxStartupView(t *testing.T) {
	t.Run("without loading items", func(t *testing.T) {
		m := cplxModel(t)
		m.loadingItems = nil
		if got := m.startupView().Content; got == "" || strings.Contains(got, "✓") {
			t.Errorf("startup view = %q", got)
		}
	})

	t.Run("with loading items", func(t *testing.T) {
		m := cplxModel(t)
		m.loadingItems = map[string]bool{"skills": true, "mcp": false}
		m.loadingTotal = 2
		got := m.startupView().Content
		if !strings.Contains(got, "✓") {
			t.Errorf("a finished item must be ticked, got %q", got)
		}
		if !strings.Contains(got, "skills") || !strings.Contains(got, "mcp") {
			t.Errorf("both items must be listed, got %q", got)
		}
		// Sorted for a stable splash.
		if strings.Index(got, "mcp") > strings.Index(got, "skills") {
			t.Errorf("items must be listed in sorted order, got %q", got)
		}
	})
}

func TestCplxSidebarRenderInput(t *testing.T) {
	t.Run("without a run", func(t *testing.T) {
		m := cplxModel(t)
		in := m.sidebarRenderInput(30, 20)
		if in.Width != 30 || in.Height != 20 {
			t.Errorf("size = %dx%d, want 30x20", in.Width, in.Height)
		}
		if in.ModelName != "test-model" {
			t.Errorf("ModelName = %q", in.ModelName)
		}
		if in.RunPhase != "" || in.RunSpec != "" {
			t.Errorf("no run must leave the run fields empty, got %+v", in)
		}
	})

	t.Run("with a run", func(t *testing.T) {
		m := cplxModel(t)
		m.run = &runState{
			phase: "running", specName: "spec-1", retries: 2, maxRetries: 10,
			checklist: []ChecklistStep{{Title: "one"}},
		}
		in := m.sidebarRenderInput(30, 20)
		if in.RunPhase != "running" || in.RunSpec != "spec-1" {
			t.Errorf("run fields = %+v", in)
		}
		// The cycle counter is one-based.
		if in.RunCycle != 3 || in.RunMaxCycle != 10 {
			t.Errorf("cycle = %d/%d, want 3/10", in.RunCycle, in.RunMaxCycle)
		}
		if len(in.RunChecklist) != 1 {
			t.Errorf("checklist = %+v", in.RunChecklist)
		}
	})

	t.Run("a run with no phase is not shown", func(t *testing.T) {
		m := cplxModel(t)
		m.run = &runState{specName: "spec-1"}
		if in := m.sidebarRenderInput(30, 20); in.RunSpec != "" {
			t.Errorf("a phaseless run must stay hidden, got %+v", in)
		}
	})
}

func TestCplxHandleLoadingTick(t *testing.T) {
	t.Run("re-arms while loading", func(t *testing.T) {
		m := cplxModel(t)
		m.loading = true
		m.loadingDots = 3
		_, cmd, handled := m.handleLoadingTick()
		if !handled || cmd == nil {
			t.Error("a tick while loading must re-arm")
		}
		if m.loadingDots != 0 {
			t.Errorf("loadingDots = %d, want the counter to wrap to 0", m.loadingDots)
		}
	})

	// The re-arm belongs to the init splash lifecycle, not to process uptime:
	// once loading ends the chain must stop.
	t.Run("stops when not loading", func(t *testing.T) {
		m := cplxModel(t)
		m.loading = false
		m.loadingDots = 1
		_, cmd, handled := m.handleLoadingTick()
		if !handled {
			t.Error("the tick must still be marked handled")
		}
		if cmd != nil {
			t.Error("a tick outside loading must not re-arm")
		}
		if m.loadingDots != 1 {
			t.Errorf("loadingDots = %d, want it untouched", m.loadingDots)
		}
	})
}

func TestCplxHandleMatrixTick(t *testing.T) {
	t.Run("running", func(t *testing.T) {
		m := cplxModel(t)
		m.running = true
		m.matrix.feed("init", 100)
		_, cmd, handled := m.handleMatrixTick()
		if !handled || cmd == nil {
			t.Error("a tick while running must re-arm")
		}
	})

	t.Run("idle", func(t *testing.T) {
		m := cplxModel(t)
		m.running = false
		_, cmd, handled := m.handleMatrixTick()
		if !handled {
			t.Error("the tick must still be marked handled")
		}
		if cmd != nil {
			t.Error("an idle tick must not re-arm")
		}
	})
}

func TestCplxHandleInputSubmit(t *testing.T) {
	t.Run("slash command dispatches", func(t *testing.T) {
		m := cplxModel(t)
		_, _, handled := m.handleInputSubmit(InputSubmitMsg{Text: "/session"})
		if !handled {
			t.Error("a submitted line must be handled")
		}
		if len(m.chatModel.Messages) == 0 ||
			!strings.Contains(m.chatModel.Messages[0].content, "Session:") {
			t.Errorf("expected the slash command to run, got %+v", m.chatModel.Messages)
		}
	})

	// A slash command typed mid-turn is dropped rather than queued.
	t.Run("slash command ignored while running", func(t *testing.T) {
		m := cplxModel(t)
		m.running = true
		_, cmd, handled := m.handleInputSubmit(InputSubmitMsg{Text: "/session"})
		if !handled || cmd != nil {
			t.Error("a slash command while running must be swallowed")
		}
		if len(m.chatModel.Messages) != 0 {
			t.Errorf("nothing may be appended, got %+v", m.chatModel.Messages)
		}
	})

	t.Run("plain text is enqueued", func(t *testing.T) {
		m := cplxModel(t)
		_, _, handled := m.handleInputSubmit(InputSubmitMsg{Text: "hello there"})
		if !handled {
			t.Error("plain text must be handled")
		}
	})
}

func TestCplxHandleKeyPressMsg_DropsResizeFragments(t *testing.T) {
	m := cplxModel(t)
	m.resizeAt = time.Now()
	// While draining, a fragment is swallowed and only re-arms the drain timer.
	_, cmd, handled := m.handleKeyPressMsg(tea.KeyPressMsg{Code: 'R', Text: "R"})
	if !handled || cmd == nil {
		t.Error("a resize fragment must be swallowed and re-arm the drain")
	}
	if m.inputModel.Text != "" {
		t.Errorf("a fragment must not reach the input, got %q", m.inputModel.Text)
	}
}

// updateTerminal reports handled=false for anything it does not own, so the
// caller can go on to the agent-event handlers.
func TestCplxUpdateTerminal_UnhandledFallsThrough(t *testing.T) {
	m := cplxModel(t)
	if _, _, handled := m.updateTerminal(struct{ nope bool }{}); handled {
		t.Error("an unknown message must not be claimed")
	}
}
