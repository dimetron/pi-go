package tui

import (
	"bytes"
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
		{"", "pi-go"},
		{"fix bug", "pi-go: fix bug"},
		{"  spaced  ", "pi-go: spaced"},
		// Control characters are replaced with spaces — this keeps the
		// OSC 0 payload printable and safe.
		{"with\nnewline", "pi-go: with newline"},
		{"with\x1bESC", "pi-go: with ESC"},
		{"with\x07BEL", "pi-go: with BEL"},
	}
	for _, c := range cases {
		got := formatTerminalTitle(c.in)
		if got != c.want {
			t.Errorf("formatTerminalTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSetTerminalTitle_WritesOSC0(t *testing.T) {
	var buf bytes.Buffer
	setTerminalTitle(&buf, "hello")
	got := buf.String()
	want := "\x1b]0;pi-go: hello\x07"
	if got != want {
		t.Errorf("setTerminalTitle wrote %q, want %q", got, want)
	}
}

func TestSetTerminalTitle_NilWriter(t *testing.T) {
	// Must not panic when writer is nil (used to skip work in tests).
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("setTerminalTitle(nil) panicked: %v", r)
		}
	}()
	setTerminalTitle(nil, "anything")
}

func TestResetTerminalTitle_WritesEmptyOSC0(t *testing.T) {
	var buf bytes.Buffer
	resetTerminalTitle(&buf)
	got := buf.String()
	want := "\x1b]0;\x07"
	if got != want {
		t.Errorf("resetTerminalTitle wrote %q, want %q", got, want)
	}
}

func TestApplySessionTitle_EmptyPrompt_NoOp(t *testing.T) {
	m, svc := newTitleTestModel(t)
	// Capture the OSC 0 write so the test doesn't pollute test output.
	var buf bytes.Buffer
	saved := defaultTitleWriter
	defaultTitleWriter = &buf
	t.Cleanup(func() { defaultTitleWriter = saved })

	m.applySessionTitle("   ")
	if svc.lastTitle() != "" {
		t.Errorf("SetSessionTitle should not be called for empty prompt, got %q", svc.lastTitle())
	}
	if buf.Len() != 0 {
		t.Errorf("OSC 0 should not be emitted for empty prompt, got %q", buf.String())
	}
}

func TestApplySessionTitle_RecordsAndEmits(t *testing.T) {
	m, svc := newTitleTestModel(t)
	var buf bytes.Buffer
	saved := defaultTitleWriter
	defaultTitleWriter = &buf
	t.Cleanup(func() { defaultTitleWriter = saved })

	m.applySessionTitle("fix the linter issue in agent.go")

	if got := svc.lastTitle(); got != "fix the linter issue in agent.go" {
		t.Errorf("SetSessionTitle recorded %q, want %q", got, "fix the linter issue in agent.go")
	}
	if !bytes.Contains(buf.Bytes(), []byte("\x1b]0;pi-go: fix the linter issue in agent.go\x07")) {
		t.Errorf("OSC 0 not emitted as expected, got %q", buf.String())
	}
}

func TestApplySessionTitle_NilAgent_StillEmitsOSC(t *testing.T) {
	// Even with no agent, the terminal title should still update — the
	// tab/window title is independent of session-metadata storage.
	m := &model{cfg: Config{}}
	var buf bytes.Buffer
	saved := defaultTitleWriter
	defaultTitleWriter = &buf
	t.Cleanup(func() { defaultTitleWriter = saved })

	m.applySessionTitle("orphan turn")
	if !bytes.Contains(buf.Bytes(), []byte("pi-go: orphan turn")) {
		t.Errorf("OSC 0 must still be emitted without an agent, got %q", buf.String())
	}
}

func TestApplySessionTitle_MultilinePrompt_TruncatesToFirstLine(t *testing.T) {
	m, svc := newTitleTestModel(t)
	var buf bytes.Buffer
	saved := defaultTitleWriter
	defaultTitleWriter = &buf
	t.Cleanup(func() { defaultTitleWriter = saved })

	m.applySessionTitle("first line\nsecond line\nthird")
	if got := svc.lastTitle(); got != "first line" {
		t.Errorf("SetSessionTitle recorded %q, want %q", got, "first line")
	}
	if !bytes.Contains(buf.Bytes(), []byte("pi-go: first line")) {
		t.Errorf("OSC 0 should contain first line only, got %q", buf.String())
	}
	if bytes.Contains(buf.Bytes(), []byte("second line")) {
		t.Errorf("OSC 0 should not contain later lines, got %q", buf.String())
	}
}
