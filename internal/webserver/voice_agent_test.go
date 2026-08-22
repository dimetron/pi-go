//go:build !windows

package webserver

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/voicegemini"
)

// fakePty returns a bridge that is "running" without a child process, plus the
// bytes written to it. Everything the voice tools do to a PTY is a write or a
// read of the capture, so this exercises the real methods without spawning pi.
type fakePty struct {
	written strings.Builder
	closed  bool
}

func (f *fakePty) Read(p []byte) (int, error)  { return 0, nil }
func (f *fakePty) Write(p []byte) (int, error) { f.written.WriteString(string(p)); return len(p), nil }
func (f *fakePty) Close() error                { f.closed = true; return nil }

// testBridge wires a PtyBridge onto a fake PTY and registers it in the pool
// under sessionID, the way a real terminal attachment would.
func testBridge(t *testing.T, s *ServerV2, sessionID string) (*PtyBridge, *fakePty) {
	t.Helper()
	f := &fakePty{}
	b := NewPtyBridge(".", "", "", nil, false, s.log)
	b.ptyFile = f
	b.cmd = fakeAliveCmd(t)
	s.ptyPool.mu.Lock()
	s.ptyPool.bridges[sessionID] = b
	s.ptyPool.mu.Unlock()
	t.Cleanup(func() { _ = b.Close() })
	return b, f
}

// fakeAliveCmd gives a bridge the child process Alive() looks for, without
// spawning pi.
//
// The child is a copy of the test binary told to run no tests, so it exits at
// once. That is enough: only startProcess starts the goroutine that closes
// `done`, so a bridge built by hand keeps reporting alive, and nothing here
// ever signals a pid this test does not own.
func fakeAliveCmd(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the stand-in child: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func voiceCall(name string, args any) voicegemini.FunctionCall {
	raw, _ := json.Marshal(args)
	return voicegemini.FunctionCall{ID: "call-1", Name: name, Args: raw}
}

func TestAgentVoiceToolsSchemas(t *testing.T) {
	tools, err := agentVoiceTools()
	if err != nil {
		t.Fatalf("agentVoiceTools() = %v", err)
	}
	want := map[string]bool{
		voiceToolSendPrompt: false,
		voiceToolReadScreen: false,
		voiceToolSendKey:    false,
		voiceToolWait:       false,
	}
	for _, tool := range tools {
		if _, ok := want[tool.Name]; !ok {
			t.Errorf("unexpected tool %q", tool.Name)
			continue
		}
		want[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("%s has no description; the model picks tools by it", tool.Name)
		}
		// A key Gemini's strict proto-JSON parser rejects closes the session at
		// setup with WebSocket 1007 — at the microphone, not at startup.
		if strings.Contains(string(tool.Parameters), "additionalProperties") {
			t.Errorf("%s schema carries additionalProperties: %s", tool.Name, tool.Parameters)
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
			t.Errorf("%s schema is not valid JSON: %v", tool.Name, err)
			continue
		}
		if schema["type"] != "object" {
			t.Errorf("%s schema type = %v, want object", tool.Name, schema["type"])
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %q was not declared", name)
		}
	}
}

// EnableVoice attaches the tools, and a build whose schemas cannot be expressed
// must fail at boot rather than at the first microphone byte.
func TestEnableVoiceAttachesAgentTools(t *testing.T) {
	s := withVoice(t, testServer(t))
	tools, err := agentVoiceTools()
	if err != nil {
		t.Fatalf("agentVoiceTools() = %v", err)
	}
	s.voiceGemini.Tools = tools

	setup, _ := json.Marshal(s.voiceGemini.SetupMessage())
	for _, name := range []string{voiceToolSendPrompt, voiceToolReadScreen, voiceToolSendKey, voiceToolWait} {
		if !strings.Contains(string(setup), name) {
			t.Errorf("the setup message does not declare %s: %s", name, setup)
		}
	}
}

func TestVoiceToolSendPrompt(t *testing.T) {
	s := withVoice(t, testServer(t))
	_, f := testBridge(t, s, "term-1")
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	res := s.executeVoiceTool(t.Context(), vs, voiceCall(voiceToolSendPrompt, map[string]string{
		"prompt": "run the unit tests",
	}))
	if _, bad := res.Response["error"]; bad {
		t.Fatalf("send_prompt failed: %v", res.Response)
	}
	got := f.written.String()
	if !strings.Contains(got, "run the unit tests") {
		t.Errorf("the prompt did not reach the pty: %q", got)
	}
	// Without the submit the prompt just sits in pi's input box.
	if !strings.HasSuffix(got, "\r") {
		t.Errorf("the prompt was not submitted: %q", got)
	}
	if res.Summary == "" {
		t.Error("no summary for the transcript panel")
	}
}

// The argument is a speech transcript routed through a language model. The one
// thing it must not be able to do is type an escape sequence into the terminal
// or submit half a sentence with a stray carriage return.
func TestVoiceToolSendPromptStripsControlBytes(t *testing.T) {
	s := withVoice(t, testServer(t))
	_, f := testBridge(t, s, "term-1")
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	s.executeVoiceTool(t.Context(), vs, voiceCall(voiceToolSendPrompt, map[string]string{
		"prompt": "delete\r\nthings\x1b[2J\x03 now",
	}))
	got := f.written.String()
	body := strings.TrimSuffix(got, "\r")
	if strings.ContainsAny(body, "\x1b\x03\r\n") {
		t.Errorf("control bytes reached the pty: %q", got)
	}
	if body != "delete things[2J now" {
		t.Errorf("sanitized prompt = %q", body)
	}
}

func TestVoiceToolSendPromptRejectsEmpty(t *testing.T) {
	s := withVoice(t, testServer(t))
	testBridge(t, s, "term-1")
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	res := s.executeVoiceTool(t.Context(), vs, voiceCall(voiceToolSendPrompt, map[string]string{"prompt": "\x00\x01"}))
	if _, bad := res.Response["error"]; !bad {
		t.Errorf("an empty prompt was accepted: %v", res.Response)
	}
}

func TestVoiceToolReadScreen(t *testing.T) {
	s := withVoice(t, testServer(t))
	b, _ := testBridge(t, s, "term-1")
	b.captureOutput([]byte("\x1b[32m✓\x1b[0m 12 tests passed\r\n"))
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	res := s.executeVoiceTool(t.Context(), vs, voiceCall(voiceToolReadScreen, map[string]any{}))
	screen, _ := res.Response["screen"].(string)
	if !strings.Contains(screen, "12 tests passed") {
		t.Errorf("screen = %q, want the agent's output", screen)
	}
	if strings.Contains(screen, "\x1b") {
		t.Errorf("styling reached the model: %q", screen)
	}
}

// A model that asks for a whole file's worth of screen would crowd the live
// conversation out of its own context.
func TestVoiceToolReadScreenClampsLines(t *testing.T) {
	s := withVoice(t, testServer(t))
	b, _ := testBridge(t, s, "term-1")
	for i := 0; i < voiceScreenMaxLines*2; i++ {
		b.captureOutput([]byte("output line\r\n"))
	}
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	res := s.executeVoiceTool(t.Context(), vs, voiceCall(voiceToolReadScreen, map[string]any{"lines": 100000}))
	screen, _ := res.Response["screen"].(string)
	if n := strings.Count(screen, "\n") + 1; n > voiceScreenMaxLines {
		t.Errorf("returned %d lines, want at most %d", n, voiceScreenMaxLines)
	}
}

func TestVoiceToolSendKey(t *testing.T) {
	s := withVoice(t, testServer(t))
	_, f := testBridge(t, s, "term-1")
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	if res := s.executeVoiceTool(t.Context(), vs, voiceCall(voiceToolSendKey, map[string]string{"key": "y"})); res.Response["error"] != nil {
		t.Fatalf("send_key: %v", res.Response)
	}
	if res := s.executeVoiceTool(t.Context(), vs, voiceCall(voiceToolSendKey, map[string]string{"key": "ctrl-c"})); res.Response["error"] != nil {
		t.Fatalf("send_key ctrl-c: %v", res.Response)
	}
	if got := f.written.String(); got != "y\x03" {
		t.Errorf("pty received %q, want \"y\\x03\"", got)
	}
}

// Named keys are an allowlist: the model must not be able to type an arbitrary
// escape sequence by calling it a key.
func TestVoiceToolSendKeyRejectsUnknown(t *testing.T) {
	s := withVoice(t, testServer(t))
	_, f := testBridge(t, s, "term-1")
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	res := s.executeVoiceTool(t.Context(), vs, voiceCall(voiceToolSendKey, map[string]string{"key": "\x1b[2J"}))
	msg, _ := res.Response["error"].(string)
	if msg == "" {
		t.Fatalf("an arbitrary sequence was accepted: %v", res.Response)
	}
	// The error names the alternatives, so the model can retry correctly.
	if !strings.Contains(msg, "enter") {
		t.Errorf("error = %q, want it to list the known keys", msg)
	}
	if f.written.Len() != 0 {
		t.Errorf("bytes reached the pty anyway: %q", f.written.String())
	}
}

func TestVoiceToolWaitReportsIdle(t *testing.T) {
	s := withVoice(t, testServer(t))
	b, _ := testBridge(t, s, "term-1")
	b.captureOutput([]byte("done\r\n"))
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	start := time.Now()
	res := s.executeVoiceTool(t.Context(), vs, voiceCall(voiceToolWait, map[string]any{"seconds": 5}))
	if res.Response["idle"] != true {
		t.Errorf("idle = %v, want true after output stopped", res.Response["idle"])
	}
	// It must return as soon as the quiet window passes, not hold the full
	// timeout: the user is waiting to be told what happened.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("waited %v for a %v quiet window", elapsed, voiceWaitQuiet)
	}
}

func TestVoiceToolWaitReportsStillWorking(t *testing.T) {
	s := withVoice(t, testServer(t))
	b, _ := testBridge(t, s, "term-1")
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				b.captureOutput([]byte("working...\r"))
				time.Sleep(20 * time.Millisecond)
			}
		}
	}()
	defer close(stop)

	res := s.executeVoiceTool(t.Context(), vs, voiceCall(voiceToolWait, map[string]any{"seconds": 1}))
	if res.Response["idle"] != false {
		t.Errorf("idle = %v, want false while the agent keeps writing", res.Response["idle"])
	}
	// The status has to tell the model what to do next, not just report a fact.
	if status, _ := res.Response["status"].(string); !strings.Contains(status, "wait again") {
		t.Errorf("status = %q, want it to steer the next turn", status)
	}
}

// Every failure is a response, never silence: a provider left waiting on a
// function response stalls the whole conversation.
func TestVoiceToolsWithoutTerminal(t *testing.T) {
	s := withVoice(t, testServer(t))

	for _, vs := range []*voiceSession{
		{ID: "vs-1"},                          // the page reported no terminal
		{ID: "vs-2", Terminal: "not-running"}, // the terminal is gone
	} {
		for _, name := range []string{voiceToolSendPrompt, voiceToolReadScreen, voiceToolSendKey, voiceToolWait} {
			res := s.executeVoiceTool(t.Context(), vs, voiceCall(name, map[string]any{"prompt": "x", "key": "y"}))
			msg, _ := res.Response["error"].(string)
			if msg == "" {
				t.Errorf("%s on %s returned no error: %v", name, vs.ID, res.Response)
			}
			// The model reads this out loud, so it has to say what to do.
			if !strings.Contains(msg, "terminal") {
				t.Errorf("%s error = %q, want it to name the terminal", name, msg)
			}
		}
	}
}

func TestVoiceToolUnknownName(t *testing.T) {
	s := withVoice(t, testServer(t))
	testBridge(t, s, "term-1")
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	res := s.executeVoiceTool(t.Context(), vs, voiceCall("pi_rm_rf", map[string]any{}))
	if res.Response["error"] == nil {
		t.Errorf("an invented tool was executed: %v", res.Response)
	}
}

// A model that omits an optional argument should get the default, not a refusal.
func TestVoiceToolsAcceptMissingArgs(t *testing.T) {
	s := withVoice(t, testServer(t))
	b, _ := testBridge(t, s, "term-1")
	b.captureOutput([]byte("hello\r\n"))
	vs := &voiceSession{ID: "vs-1", Terminal: "term-1"}

	res := s.executeVoiceTool(t.Context(), vs, voicegemini.FunctionCall{Name: voiceToolReadScreen})
	if res.Response["error"] != nil {
		t.Errorf("read_screen with no args failed: %v", res.Response)
	}
}

func TestVoiceInstructionsCarryLiveContext(t *testing.T) {
	s := NewServerV2(Config{Addr: "127.0.0.1:0", Project: "/tmp/some-project", Model: "claude-sonnet-5"})
	b, _ := testBridge(t, s, "term-1")
	b.captureOutput([]byte("pi > waiting for input\r\n"))

	got := s.voiceInstructions(&voiceSession{ID: "vs-1", Terminal: "term-1"})
	for _, want := range []string{
		"/tmp/some-project",          // which project pi is working in
		"claude-sonnet-5",            // which model it runs
		voiceToolSendPrompt,          // how to drive it
		"pi > waiting for input",     // what its terminal already shows
		"Never describe pi's output", // and the rule that keeps it honest
	} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions do not mention %q:\n%s", want, got)
		}
	}
}

// A session that cannot reach a terminal must say so in the instruction, or the
// model discovers it only when a tool fails mid-sentence.
func TestVoiceInstructionsSayWhenUnbound(t *testing.T) {
	s := withVoice(t, testServer(t))

	if got := s.voiceInstructions(&voiceSession{ID: "vs-1"}); !strings.Contains(got, "not attached") {
		t.Errorf("instructions for an unbound session do not say so:\n%s", got)
	}
	if got := s.voiceInstructions(&voiceSession{ID: "vs-2", Terminal: "gone"}); !strings.Contains(got, "not running") {
		t.Errorf("instructions for a dead terminal do not say so:\n%s", got)
	}
}

// The instruction is per session because it carries live state; it must never
// become every session's instruction.
func TestSessionInstructionsDoNotLeakAcrossSessions(t *testing.T) {
	shared := voicegemini.New("AIzaSyTestKeyLongEnough")
	one := shared.WithSessionInstructions("session one context")
	two := shared.WithSessionInstructions("session two context")

	if shared.Instructions != "" {
		t.Errorf("the shared creator was mutated: %q", shared.Instructions)
	}
	if one.Instructions == two.Instructions {
		t.Errorf("both sessions got %q", one.Instructions)
	}
	if got := shared.WithSessionInstructions(""); got != shared {
		t.Error("an empty instruction should be a no-op, not a copy")
	}
}
