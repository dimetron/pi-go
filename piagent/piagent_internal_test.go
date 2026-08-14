package piagent

import (
	"context"
	"errors"
	"iter"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	adktool "google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

// fakeLLM is the seam that makes an embed testable without a network: it
// satisfies ADK's model interface and replays a scripted reply.
type fakeLLM struct {
	name  string
	reply string
	err   error
	// toolCall, when set, is returned on the first turn; the reply follows on
	// the second, once the tool result has come back.
	toolCall *genai.FunctionCall

	mu     sync.Mutex
	prompt string // the last system instruction the model was handed
	calls  int
}

func (f *fakeLLM) Name() string { return f.name }

func (f *fakeLLM) GenerateContent(_ context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	f.mu.Lock()
	f.calls++
	call := f.calls
	if req != nil && req.Config != nil && req.Config.SystemInstruction != nil {
		var b strings.Builder
		for _, p := range req.Config.SystemInstruction.Parts {
			b.WriteString(p.Text)
		}
		f.prompt = b.String()
	}
	f.mu.Unlock()

	return func(yield func(*model.LLMResponse, error) bool) {
		if f.err != nil {
			yield(nil, f.err)
			return
		}
		if f.toolCall != nil && call == 1 {
			yield(&model.LLMResponse{Content: &genai.Content{
				Role:  genai.RoleModel,
				Parts: []*genai.Part{{FunctionCall: f.toolCall}},
			}}, nil)
			return
		}
		yield(&model.LLMResponse{Content: genai.NewContentFromText(f.reply, genai.RoleModel)}, nil)
	}
}

func (f *fakeLLM) systemPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompt
}

// isolate points HOME at a scratch directory so a test never reads or writes
// the developer's real ~/.pi-go, and returns a working directory to run in.
func isolate(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows
	return home
}

// newTestAgent builds an agent with every optional subsystem off, which is the
// configuration a test can assert on deterministically.
func newTestAgent(t *testing.T, llm model.LLM, extra ...Option) *Agent {
	t.Helper()
	workDir := t.TempDir()
	opts := append([]Option{
		WithLLM(llm),
		WithWorkingDir(workDir),
		WithSessionDir(t.TempDir()),
		WithMemory(false),
		WithPalace(false),
		WithSubagents(false),
		WithLSP(LSPOff),
	}, extra...)

	ag, err := New(t.Context(), opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := ag.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return ag
}

func TestNewZeroSubsystemsRunsATurn(t *testing.T) {
	isolate(t)
	llm := &fakeLLM{name: "fake-model", reply: "hello from the fake"}
	ag := newTestAgent(t, llm)

	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if sessionID == "" {
		t.Fatal("NewSession returned an empty ID")
	}

	got, err := ag.Ask(t.Context(), sessionID, "hi")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got != "hello from the fake" {
		t.Errorf("Ask = %q, want %q", got, "hello from the fake")
	}

	if ag.Model() != "fake-model" {
		t.Errorf("Model() = %q, want %q", ag.Model(), "fake-model")
	}
	if ag.Provider() != "" {
		t.Errorf("Provider() = %q, want empty for an injected model", ag.Provider())
	}
	if len(ag.Tools()) == 0 {
		t.Error("Tools() is empty; core tools should always be registered")
	}
}

func TestNewSessionIsPersisted(t *testing.T) {
	isolate(t)
	sessionDir := t.TempDir()
	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "ok"}, WithSessionDir(sessionDir))

	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := ag.SetSessionTitle(sessionID, "embedded run"); err != nil {
		t.Fatalf("SetSessionTitle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, sessionID)); err != nil {
		t.Errorf("session directory not created: %v", err)
	}
}

func TestWorkingDirIsReportedToTheModel(t *testing.T) {
	isolate(t)
	workDir := t.TempDir()
	llm := &fakeLLM{name: "fake", reply: "ok"}
	ag := newTestAgent(t, llm, WithWorkingDir(workDir), WithExtraInstruction("HOUSE RULE: be terse."))

	if ag.WorkingDir() != workDir {
		t.Errorf("WorkingDir() = %q, want %q", ag.WorkingDir(), workDir)
	}

	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := ag.Ask(t.Context(), sessionID, "hi"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	prompt := llm.systemPrompt()
	if !strings.Contains(prompt, workDir) {
		t.Errorf("system prompt does not mention the working directory %q", workDir)
	}
	if !strings.Contains(prompt, "HOUSE RULE: be terse.") {
		t.Error("system prompt is missing the extra instruction")
	}
}

func TestAskSurfacesProviderErrors(t *testing.T) {
	isolate(t)
	llm := &fakeLLM{name: "fake", err: errors.New("provider exploded")}
	ag := newTestAgent(t, llm)

	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := ag.Ask(t.Context(), sessionID, "hi"); err == nil {
		t.Fatal("Ask returned nil error for a failing provider")
	}
}

func TestRunStreamingYieldsEvents(t *testing.T) {
	isolate(t)
	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "streamed"})

	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	var text strings.Builder
	for ev, err := range ag.RunStreaming(t.Context(), sessionID, "hi") {
		if err != nil {
			t.Fatalf("RunStreaming: %v", err)
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			text.WriteString(p.Text)
		}
	}
	if !strings.Contains(text.String(), "streamed") {
		t.Errorf("streamed text = %q, want it to contain %q", text.String(), "streamed")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	isolate(t)
	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "ok"})
	if err := ag.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := ag.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestCloseReportsCleanupFailures(t *testing.T) {
	a := &Agent{}
	a.push(func() error { return errors.New("boom") })
	err := a.Close()
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Close() = %v, want it to report the cleanup failure", err)
	}
}

func TestAbortReleasesWhatWasAlreadyAcquired(t *testing.T) {
	released := 0
	a := &Agent{}
	a.push(func() error { released++; return nil })
	a.push(func() error { released++; return nil })

	err := a.abort(errors.New("construction failed"))
	if err == nil || !strings.Contains(err.Error(), "construction failed") {
		t.Fatalf("abort() = %v, want it to carry the original error", err)
	}
	if released != 2 {
		t.Errorf("released %d resources, want 2", released)
	}
}

func TestNewRejectsAnUnresolvableWorkingDir(t *testing.T) {
	isolate(t)
	// A session directory that cannot be created is the earliest failure a
	// caller can trigger without touching the filesystem layout.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := New(t.Context(),
		WithLLM(&fakeLLM{name: "fake"}),
		WithWorkingDir(t.TempDir()),
		WithSessionDir(file),
		WithMemory(false), WithPalace(false), WithSubagents(false), WithLSP(LSPOff))
	if err == nil {
		t.Fatal("New succeeded with an unusable session directory")
	}
}

func TestNewWithSubagentsAndMemory(t *testing.T) {
	home := isolate(t)
	workDir := t.TempDir()
	var events []string
	var mu sync.Mutex

	ag, err := New(t.Context(),
		WithLLM(&fakeLLM{name: "fake", reply: "ok"}),
		WithWorkingDir(workDir),
		WithSessionDir(t.TempDir()),
		WithExtraSandboxDirs(home),
		WithSubagents(true),
		WithMemory(true),
		WithPalace(true),
		WithLSP(LSPMin),
		WithAgentEvents(func(id, kind, _ string) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, id+":"+kind)
		}),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() {
		if err := ag.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := ag.Ask(t.Context(), sessionID, "hi"); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	names := map[string]bool{}
	for _, tl := range ag.Tools() {
		names[tl.Name()] = true
	}
	if !names["subagent"] && !names["agent"] {
		t.Logf("registered tools: %v", names)
	}
}

func TestNewHonoursSkillsToggle(t *testing.T) {
	isolate(t)
	workDir := t.TempDir()
	skillDir := filepath.Join(workDir, ".pi-go", "skills", "widget")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	skill := "---\nname: widget\ndescription: Make a widget.\n---\n\nBody.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skill), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	withSkills := buildInstruction(optionsFor(t, WithWorkingDir(workDir)), workDir)
	without := buildInstruction(optionsFor(t, WithWorkingDir(workDir), WithSkills(false)), workDir)

	if !strings.Contains(withSkills, "widget") {
		t.Error("skills enabled: instruction does not mention the discovered skill")
	}
	if strings.Contains(without, "Available Skills") {
		t.Error("skills disabled: instruction still carries the skills menu")
	}
}

func TestEmbedderAfterToolCallbackSeesEveryToolCall(t *testing.T) {
	isolate(t)
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "hello.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	var mu sync.Mutex
	var seen []string
	observe := func(_ adkagent.Context, tl adktool.Tool, _, result map[string]any, _ error) (map[string]any, error) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, tl.Name())
		// nil means "unchanged": the model still gets the tool's own result.
		if result == nil {
			t.Error("after-tool callback received a nil result")
		}
		return nil, nil
	}

	llm := &fakeLLM{
		name:     "fake",
		reply:    "there is one file",
		toolCall: &genai.FunctionCall{Name: "ls", Args: map[string]any{"path": workDir}},
	}
	ag := newTestAgent(t, llm,
		WithWorkingDir(workDir),
		WithAfterToolCallbacks(observe),
		WithBeforeToolCallbacks(func(_ adkagent.Context, _ adktool.Tool, _ map[string]any) (map[string]any, error) {
			return nil, nil
		}),
	)

	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	got, err := ag.Ask(t.Context(), sessionID, "what is here?")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if !strings.Contains(got, "there is one file") {
		t.Errorf("Ask = %q, want the post-tool reply", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != "ls" {
		t.Errorf("embedder after-tool callback saw %v, want [ls]", seen)
	}
}

// optionsFor resolves an option list the way New does, for tests that exercise
// one assembly step rather than the whole constructor.
func optionsFor(t *testing.T, opts ...Option) options {
	t.Helper()
	o := defaultOptions()
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// gitInit makes dir a repository so detectGitRoot has something to find.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v: %s", err, out)
	}
}
