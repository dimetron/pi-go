package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/agent"
)

// recordingSessionService is a session.Service that records SetSessionTitle
// calls. It satisfies the titleNamer interface, so the *agent.Agent wrapper
// will forward calls to it.
type recordingSessionService struct {
	mu     sync.Mutex
	titles []string
}

func (r *recordingSessionService) Create(ctx context.Context, req *session.CreateRequest) (*session.CreateResponse, error) {
	return &session.CreateResponse{Session: &fakeTitleSession{id: req.SessionID}}, nil
}
func (r *recordingSessionService) Get(ctx context.Context, req *session.GetRequest) (*session.GetResponse, error) {
	return &session.GetResponse{Session: &fakeTitleSession{id: req.SessionID}}, nil
}
func (r *recordingSessionService) List(ctx context.Context, req *session.ListRequest) (*session.ListResponse, error) {
	return &session.ListResponse{}, nil
}
func (r *recordingSessionService) Delete(ctx context.Context, req *session.DeleteRequest) error {
	return nil
}
func (r *recordingSessionService) AppendEvent(ctx context.Context, s session.Session, ev *session.Event) error {
	return nil
}
func (r *recordingSessionService) SetSessionTitle(sessionID, title string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.titles = append(r.titles, title)
	return nil
}
func (r *recordingSessionService) lastTitle() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.titles) == 0 {
		return ""
	}
	return r.titles[len(r.titles)-1]
}

type fakeTitleSession struct{ id string }

func (f *fakeTitleSession) ID() string                { return f.id }
func (f *fakeTitleSession) AppName() string           { return "pi-go" }
func (f *fakeTitleSession) UserID() string            { return "local" }
func (f *fakeTitleSession) LastUpdateTime() time.Time { return time.Time{} }
func (f *fakeTitleSession) State() session.State      { return nil }
func (f *fakeTitleSession) Events() session.Events    { return nil }

// newTitleTestModel builds a model wired to a recording session service. The
// agent will forward SetSessionTitle calls to it.
func newTitleTestModel(t *testing.T) (*model, *recordingSessionService) {
	t.Helper()
	svc := &recordingSessionService{}
	ag, err := agent.New(agent.Config{
		Model:          &stubLLM{name: "stub"},
		SessionService: svc,
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m := &model{
		cfg: Config{
			Agent:     ag,
			SessionID: "test-session",
		},
		ctx:          ctx,
		cancel:       cancel,
		inputModel:   NewInputModel(make([]HistoryEntry, 0), nil, nil, ""),
		chatModel:    ChatModel{},
		themeManager: NewThemeManager(),
		face:         NewFaceRenderer(),
		width:        100,
		height:       30,
	}
	return m, svc
}

func TestDeriveSessionTitle_FirstLine(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello world", "hello world"},
		{"first line\nsecond line", "first line"},
		{"   ", ""},
		{"  trimmed  ", "trimmed"},
		{"a\nb\nc", "a"},
		{"", ""},
	}
	for _, c := range cases {
		got := deriveSessionTitle(c.in)
		if got != c.want {
			t.Errorf("deriveSessionTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDeriveSessionTitle_Truncates(t *testing.T) {
	long := strings.Repeat("x", terminalTitleMax+10)
	got := deriveSessionTitle(long)
	// Compare by rune count, not byte length, because the ellipsis suffix
	// is multi-byte UTF-8.
	if gotRunes := len([]rune(got)); gotRunes > terminalTitleMax {
		t.Errorf("title rune count = %d, want <= %d", gotRunes, terminalTitleMax)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated title should end with ellipsis, got %q", got)
	}
}

func TestFormatTerminalTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "π -"},
		{"fix bug", "π - fix bug"},
		{"  spaced  ", "π - spaced"},
		// Control characters are replaced with spaces — this keeps the
		// OSC 0 payload printable and safe.
		{"with\nnewline", "π - with newline"},
		{"with\x1bESC", "π - with ESC"},
		{"with\x07BEL", "π - with BEL"},
	}
	for _, c := range cases {
		got := formatTerminalTitle(c.in)
		if got != c.want {
			t.Errorf("formatTerminalTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatTerminalTitleWithCWD covers the context-aware shape that the
// View() path now uses. The CWD is folded in as a fixed anchor so different
// sessions in different repos are visually distinguishable in the tab bar,
// while the slash command / prompt follows after a " | " separator.
func TestFormatTerminalTitleWithCWD(t *testing.T) {
	cases := []struct {
		name  string
		title string
		cwd   string
		want  string
	}{
		// Empty cwd keeps the legacy "π - <title>" shape so callers that
		// never set WorkDir (the unit tests for View()) see the old output.
		{"empty cwd, empty title", "", "", "π -"},
		{"empty cwd, short title", "/clear", "", "π - /clear"},
		{"empty cwd, long title is preserved by old shape", "fix the linter issue in agent.go", "", "π - fix the linter issue in agent.go"},

		// Set cwd but no command — just the CWD anchor.
		{"cwd only", "", "/home/dev/pi-go", "π - pi-go"},

		// Cwd + command is the new context-aware shape.
		{"cwd + command", "/clear", "/home/dev/pi-go", "π - pi-go | /clear"},
		{"cwd + slash with args", "/model gpt-5.4", "/home/dev/pi-go", "π - pi-go | /model gpt-5.4"},
		{"cwd + prompt-derived", "fix the linter issue in agent.go", "/home/dev/pi-go", "π - pi-go | fix the linter issue in agent.go"},

		// Trimming of whitespace must not break the CWD basename extraction.
		{"cwd padded", "/clear", "  /home/dev/pi-go  ", "π - pi-go | /clear"},

		// Control characters are scrubbed the same way as the old path.
		{"control in command is replaced", "with\nnewline", "/home/dev/pi-go", "π - pi-go | with newline"},

		// The command is "stripped" — capped at terminalTitleCmdMax runes
		// so a 200-char prompt does not eat the entire tab width on top
		// of the CWD prefix.
		{"long command is stripped", strings.Repeat("x", terminalTitleCmdMax+20), "/home/dev/pi-go",
			"π - pi-go | " + strings.Repeat("x", terminalTitleCmdMax-1) + "…"},

		// The CWD basename must go through the same control-character scrub
		// as the command, otherwise a directory whose name contains ESC or
		// BEL could terminate or inject into the OSC 0 envelope. After the
		// scrub, an all-control folder resolves to "" and the title falls
		// back to the no-context shape.
		{"folder with control char is scrubbed", "/clear", "/tmp/evil\x1b]0;PWNED\x07",
			"π - evil ]0;PWNED | /clear"},
		{"folder that is all controls falls back", "/clear", "/tmp/\x1b\x07",
			"π - /clear"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatTerminalTitleWithCWD(c.title, c.cwd)
			if got != c.want {
				t.Errorf("formatTerminalTitleWithCWD(%q, %q) = %q, want %q", c.title, c.cwd, got, c.want)
			}
		})
	}
}

// TestView_CarriesWindowTitle_WithCWD exercises the new View() wiring: when
// the model has a WorkDir, the OSC 0 payload carries the CWD anchor alongside
// the prompt-derived title so the tab bar shows "π - <folder> | <title>".
func TestView_CarriesWindowTitle_WithCWD(t *testing.T) {
	m, _ := newTitleTestModel(t)
	m.cfg.WorkDir = "/home/dev/pi-go"

	if got := m.View().WindowTitle; got != "π - pi-go" {
		t.Errorf("WindowTitle before any prompt = %q, want %q", got, "π - pi-go")
	}

	m.applySessionTitle("fix the top-level render")
	if got := m.View().WindowTitle; got != "π - pi-go | fix the top-level render" {
		t.Errorf("WindowTitle = %q, want %q", got, "π - pi-go | fix the top-level render")
	}
}

func TestApplySessionTitle_EmptyPrompt_NoOp(t *testing.T) {
	m, svc := newTitleTestModel(t)

	m.applySessionTitle("   ")
	if svc.lastTitle() != "" {
		t.Errorf("SetSessionTitle should not be called for empty prompt, got %q", svc.lastTitle())
	}
	if m.sessionTitle != "" {
		t.Errorf("sessionTitle should stay empty for empty prompt, got %q", m.sessionTitle)
	}
}

func TestApplySessionTitle_RecordsOnSessionAndModel(t *testing.T) {
	m, svc := newTitleTestModel(t)

	m.applySessionTitle("fix the linter issue in agent.go")

	if got := svc.lastTitle(); got != "fix the linter issue in agent.go" {
		t.Errorf("SetSessionTitle recorded %q, want %q", got, "fix the linter issue in agent.go")
	}
	if got := m.sessionTitle; got != "fix the linter issue in agent.go" {
		t.Errorf("sessionTitle = %q, want %q", got, "fix the linter issue in agent.go")
	}
}

func TestApplySessionTitle_NilAgent_StillSetsModelTitle(t *testing.T) {
	// Even with no agent, the terminal title should still update — the
	// tab/window title is independent of session-metadata storage.
	m := &model{cfg: Config{}}

	m.applySessionTitle("orphan turn")
	if got := m.sessionTitle; got != "orphan turn" {
		t.Errorf("sessionTitle = %q, want %q", got, "orphan turn")
	}
}

func TestApplySessionTitle_MultilinePrompt_TruncatesToFirstLine(t *testing.T) {
	m, svc := newTitleTestModel(t)

	m.applySessionTitle("first line\nsecond line\nthird")
	if got := svc.lastTitle(); got != "first line" {
		t.Errorf("SetSessionTitle recorded %q, want %q", got, "first line")
	}
	if got := m.sessionTitle; got != "first line" {
		t.Errorf("sessionTitle = %q, want %q", got, "first line")
	}
}

// The title must reach the terminal through Bubble Tea's View, never through a
// direct write to os.Stdout: the renderer owns stdout, and an out-of-band write
// lands in the middle of a frame and corrupts it.
func TestView_CarriesWindowTitle(t *testing.T) {
	m, _ := newTitleTestModel(t)

	if got := m.View().WindowTitle; got != "π -" {
		t.Errorf("WindowTitle before any prompt = %q, want %q", got, "π -")
	}

	m.applySessionTitle("fix the top-level render")
	if got := m.View().WindowTitle; got != "π - fix the top-level render" {
		t.Errorf("WindowTitle = %q, want %q", got, "π - fix the top-level render")
	}
}

// The rendered frame itself must never contain an OSC title sequence — that
// would mean the title is being written into the frame body rather than being
// carried as View metadata.
func TestView_FrameHasNoOSCTitleSequence(t *testing.T) {
	m, _ := newTitleTestModel(t)
	m.applySessionTitle("some task")

	if strings.Contains(m.View().Content, "\x1b]0;") {
		t.Error("frame body contains an OSC 0 sequence; the title must travel via View.WindowTitle")
	}
}

// When the deferred-init goroutine hands the TUI a freshly created session,
// the default title (git repo / CWD basename) the agent applied should seed
// m.sessionTitle — so the very first View() already emits "π - <default>"
// as the OSC 0 payload, before any user prompt arrives.
func TestHandleInitEvent_SeedsDefaultTitle(t *testing.T) {
	// width must be set: View() takes a fast path before the first
	// WindowSizeMsg and skips WindowTitle entirely, but by the time
	// deferred-init completes the program has delivered the size.
	m := &model{
		width:        80,
		height:       24,
		loading:      true,
		loadingItems: map[string]bool{},
		initCh:       make(chan InitEvent),
		inputModel:   NewInputModel(nil, nil, nil, ""),
	}

	msg := initEventMsg{
		event: InitEvent{
			Done: true,
			Result: &InitResult{
				SessionTitle: "piname",
			},
		},
		ch: m.initCh,
	}
	newM, _ := m.handleInitEvent(msg)
	mm := newM.(*model)

	if got := mm.sessionTitle; got != "piname" {
		t.Errorf("sessionTitle after init = %q, want %q", got, "piname")
	}
	// View() must carry the title so Bubble Tea's renderer emits the OSC 0
	// envelope in-band with the next frame. formatTerminalTitle is what
	// applies the "π - " prefix and strips control characters.
	if got := mm.View().WindowTitle; got != "π - piname" {
		t.Errorf("WindowTitle after init = %q, want %q", got, "π - piname")
	}
}

// A prompt-driven title (set via applySessionTitle) should win over the
// default seeded at init time, so a /plan session that creates its own
// title-bearing fresh session still has the expected behavior — the more
// recent user signal takes precedence.
func TestHandleInitEvent_DoesNotOverwriteExistingTitle(t *testing.T) {
	m := &model{
		loading:      true,
		loadingItems: map[string]bool{},
		initCh:       make(chan InitEvent),
		sessionTitle: "kept from a previous turn",
	}

	msg := initEventMsg{
		event: InitEvent{
			Done: true,
			Result: &InitResult{
				SessionTitle: "should-not-overwrite",
			},
		},
		ch: m.initCh,
	}
	newM, _ := m.handleInitEvent(msg)
	mm := newM.(*model)

	if got := mm.sessionTitle; got != "kept from a previous turn" {
		t.Errorf("sessionTitle = %q, want pre-existing value preserved", got)
	}
}

// /clear must reset the terminal window/tab title to the app default so the
// next View() emits the OSC 0 sequence with the default payload ("π -")
// instead of the title derived from the previous turn's prompt.
func TestHandleSlashCommand_Clear_ResetsWindowTitleToDefault(t *testing.T) {
	m, svc := newTitleTestModel(t)

	// Seed a prompt-derived title the way a real turn would.
	m.applySessionTitle("fix the linter issue in agent.go")
	if got := m.sessionTitle; got != "fix the linter issue in agent.go" {
		t.Fatalf("setup: sessionTitle = %q, want prompt-derived value", got)
	}
	if got := m.View().WindowTitle; got != "π - fix the linter issue in agent.go" {
		t.Fatalf("setup: WindowTitle = %q, want prompt-derived title", got)
	}

	// Run /clear and check both that the model field is reset and that the
	// session metadata was cleared so meta.json stays in sync with the tab.
	newM, _ := m.handleSlashCommand("/clear")
	mm := newM.(*model)

	if got := mm.sessionTitle; got != "" {
		t.Errorf("after /clear: sessionTitle = %q, want empty (default)", got)
	}
	if got := mm.View().WindowTitle; got != "π -" {
		t.Errorf("after /clear: WindowTitle = %q, want %q (default)", got, "π -")
	}
	if got := svc.lastTitle(); got != "" {
		t.Errorf("after /clear: SetSessionTitle recorded %q, want empty (default)", got)
	}
}

// Slash commands (other than /clear, /exit, /quit) should make the typed
// command the session title so the terminal tab/window reflects what the user
// just invoked. The same routing also persists the title via the agent's
// SetSessionTitle, keeping meta.json in sync with the next View()'s OSC 0
// envelope.
func TestHandleSlashCommand_UpdatesWindowTitleToCommand(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"single token", "/help"},
		{"with args", "/model gpt-5.4"},
		{"unknown command still titles", "/foobar"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, svc := newTitleTestModel(t)

			newM, _ := m.handleSlashCommand(tc.input)
			mm := newM.(*model)

			if got := mm.sessionTitle; got != tc.input {
				t.Errorf("sessionTitle = %q, want %q", got, tc.input)
			}
			if got := mm.View().WindowTitle; got != "π - "+tc.input {
				t.Errorf("WindowTitle = %q, want %q", got, "π - "+tc.input)
			}
			if got := svc.lastTitle(); got != tc.input {
				t.Errorf("SetSessionTitle recorded %q, want %q", got, tc.input)
			}
		})
	}
}

// /exit and /quit must NOT mutate the session title: the program is about to
// tear down, so any OSC 0 emitted for the new title would be a useless frame.
// The existing prompt-derived title (or the seed default) should be preserved.
func TestHandleSlashCommand_ExitAndQuit_DoNotChangeTitle(t *testing.T) {
	for _, cmd := range []string{"/exit", "/quit"} {
		t.Run(cmd, func(t *testing.T) {
			m, _ := newTitleTestModel(t)
			// Seed a known title the way a previous turn would have.
			m.applySessionTitle("fix bug in foo.go")

			// /exit returns (model, tea.Quit); we don't care about the cmd here,
			// only that the title was preserved.
			newM, _ := m.handleSlashCommand(cmd)
			mm := newM.(*model)

			if got := mm.sessionTitle; got != "fix bug in foo.go" {
				t.Errorf("%s: sessionTitle = %q, want preserved %q", cmd, got, "fix bug in foo.go")
			}
		})
	}
}

// While a turn runs, the title's prefix symbol rotates so a backgrounded tab
// shows the session is still working. Idle keeps the static "π -".
func TestTerminalTitlePrefix(t *testing.T) {
	if got := terminalTitlePrefix(false, 2); got != "π -" {
		t.Errorf("idle prefix = %q, want %q", got, "π -")
	}
	seen := map[string]bool{}
	for i := range terminalTitleWorkingSymbols {
		got := terminalTitlePrefix(true, i)
		if len([]rune(got)) != 3 || !strings.HasPrefix(got, "π ") {
			t.Errorf("running prefix at spin %d = %q, want %q + one symbol", i, got, "π ")
		}
		seen[got] = true
	}
	if len(seen) != len(terminalTitleWorkingSymbols) {
		t.Errorf("running prefixes = %v, want one distinct prefix per symbol", seen)
	}
	// An out-of-range phase must wrap rather than panic — spin comes from the
	// model and the render path has no business panicking on it.
	if got := terminalTitlePrefix(true, len(terminalTitleWorkingSymbols)); got != terminalTitlePrefix(true, 0) {
		t.Errorf("prefix did not wrap: %q", got)
	}
	if got := terminalTitlePrefix(true, -1); got != terminalTitlePrefix(true, len(terminalTitleWorkingSymbols)-1) {
		t.Errorf("negative spin did not wrap: %q", got)
	}
}

// The phase is a pure function of wall-clock time, so any redraw — whatever
// triggered it — shows the symbol the elapsed time calls for.
func TestTerminalTitleSpinIndex_AdvancesWithTime(t *testing.T) {
	base := time.UnixMilli(0)
	for i := range terminalTitleWorkingSymbols {
		at := base.Add(time.Duration(i) * terminalTitleSpinPeriod)
		if got := terminalTitleSpinIndex(at); got != i {
			t.Errorf("spin index at +%v = %d, want %d", at.Sub(base), got, i)
		}
	}
	// Within one period the phase holds steady.
	if got := terminalTitleSpinIndex(base.Add(terminalTitleSpinPeriod - time.Millisecond)); got != 0 {
		t.Errorf("spin index just before the first rollover = %d, want 0", got)
	}
	// And it wraps back to the start of the cycle.
	full := time.Duration(len(terminalTitleWorkingSymbols)) * terminalTitleSpinPeriod
	if got := terminalTitleSpinIndex(base.Add(full)); got != 0 {
		t.Errorf("spin index after a full cycle = %d, want 0", got)
	}
}

// The animated prefix has to reach the terminal the same way the static one
// does — through View().WindowTitle, with the session title still attached.
func TestView_WindowTitle_AnimatesWhileRunning(t *testing.T) {
	m, _ := newTitleTestModel(t)
	m.applySessionTitle("fix the top-level render")

	m.running = true
	for i := range terminalTitleWorkingSymbols {
		m.titleSpin = i
		want := terminalTitlePrefix(true, i) + " fix the top-level render"
		if got := m.View().WindowTitle; got != want {
			t.Errorf("running WindowTitle at spin %d = %q, want %q", i, got, want)
		}
	}

	// Turn over: the prefix goes back to being static, whatever phase the
	// animation stopped on.
	m.running = false
	if got := m.View().WindowTitle; got != "π - fix the top-level render" {
		t.Errorf("idle WindowTitle = %q, want %q", got, "π - fix the top-level render")
	}
}

// The matrix tick is what drives the animation: it already advances the tool
// bullet's blink while running, and now the title phase alongside it.
func TestMatrixTick_AdvancesTitleSpin(t *testing.T) {
	m, _ := newTitleTestModel(t)
	m.running = true
	m.titleSpin = -1

	updated, _ := m.Update(matrixTickMsg{})
	mm, ok := updated.(*model)
	if !ok {
		t.Fatalf("Update returned %T, want *model", updated)
	}
	if want := terminalTitleSpinIndex(time.Now()); mm.titleSpin != want {
		t.Errorf("titleSpin after tick = %d, want %d", mm.titleSpin, want)
	}
}
