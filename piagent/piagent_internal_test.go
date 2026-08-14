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

	"github.com/dimetron/pi-go/internal/agent"
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

func (f *fakeLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
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
		WithModel(llm),
		WithWorkingDir(workDir),
		WithSessionDir(t.TempDir()),
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

func TestNewRequiresAModel(t *testing.T) {
	isolate(t)
	_, err := New(t.Context(), WithWorkingDir(t.TempDir()))
	if !errors.Is(err, ErrNoModel) {
		t.Fatalf("New without a model = %v, want ErrNoModel", err)
	}
	// The error has to send the reader somewhere, since this package cannot
	// build a model for them.
	if !strings.Contains(err.Error(), "WithModel") || !strings.Contains(err.Error(), "pimodels") {
		t.Errorf("error = %q, want it to name both the option and the models package", err)
	}
}

// selfNamingLLM is a model that reports its own provider, mirroring how the
// models package wraps one: a struct embedding model.LLM, with Provider() on
// the value receiver — so a value, not just a pointer, satisfies the shape.
// That is the form that actually ships; a pointer-only method would leave
// every consumer silently on the fallback path.
//
// Asserting the shape rather than a named type is what keeps piagent
// independent of that package.
type selfNamingLLM struct {
	model.LLM
	provider string
}

func (s selfNamingLLM) Provider() string { return s.provider }

// TestProviderAssertionDrivesToolRegistration is the end-to-end half of the
// contract: a model that says it is Gemini gets the grounding tool even though
// nothing about its name suggests it. If the shape assertion ever stops
// matching what the models package returns, this fails while the unit test on
// providerOf keeps passing.
func TestProviderAssertionDrivesToolRegistration(t *testing.T) {
	isolate(t)

	hasGrounding := func(ag *Agent) bool {
		for _, tl := range ag.Tools() {
			if tl.Name() == agent.GroundingToolName {
				return true
			}
		}
		return false
	}

	named := newTestAgent(t, selfNamingLLM{
		LLM:      &fakeLLM{name: "internal-codename-7", reply: "ok"},
		provider: "gemini",
	})
	if !hasGrounding(named) {
		t.Error("a model reporting Provider() == gemini did not get the grounding tool")
	}
}

// TestModelWithoutProviderStillBuilds pins the not-ok branch. Every model built
// outside the models package lands here — a hand-rolled ADK model, a test fake,
// anything wrapping a third-party client — so this is the path that breaks in
// someone else's codebase rather than in ours.
func TestModelWithoutProviderStillBuilds(t *testing.T) {
	isolate(t)

	plain := &fakeLLM{name: "internal-codename-7", reply: "still works"}
	if _, ok := any(plain).(interface{ Provider() string }); ok {
		t.Fatal("the fake reports a provider; this test needs one that cannot")
	}

	ag := newTestAgent(t, plain)
	sessionID, err := ag.NewSession(t.Context())
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	got, err := ag.Ask(t.Context(), sessionID, "hi")
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if got != "still works" {
		t.Errorf("Ask = %q, want the turn to complete normally", got)
	}
	// An unrecognized name yields no provider, and the degradation is silent:
	// a raw span attribute and no grounding tool, never a failure to build.
	if got := providerOf(plain); got != "" {
		t.Errorf("providerOf = %q, want empty for an unrecognizable model", got)
	}
}

func TestProviderOfPrefersTheModelsOwnAnswer(t *testing.T) {
	// A model that knows its provider is believed even when its name says
	// otherwise — that is the point of asking it.
	m := selfNamingLLM{LLM: &fakeLLM{name: "claude-sonnet-5"}, provider: "bedrock"}
	if got := providerOf(m); got != "bedrock" {
		t.Errorf("providerOf(self-naming) = %q, want %q", got, "bedrock")
	}

	// An empty answer is not an answer; fall back to the name.
	empty := selfNamingLLM{LLM: &fakeLLM{name: "claude-sonnet-5"}}
	if got := providerOf(empty); got != "anthropic" {
		t.Errorf("providerOf(empty answer) = %q, want the name-derived %q", got, "anthropic")
	}

	// A model that cannot say falls back to the name too.
	if got := providerOf(&fakeLLM{name: "gemini-3-pro"}); got != "gemini" {
		t.Errorf("providerOf(plain model) = %q, want %q", got, "gemini")
	}
	if got := providerOf(&fakeLLM{name: "some-gateway-model"}); got != "" {
		t.Errorf("providerOf(unrecognized) = %q, want empty", got)
	}
}

func TestProviderFromModelName(t *testing.T) {
	tests := map[string]string{
		"claude-sonnet-5":    "anthropic",
		"gpt-5.6-sol":        "openai",
		"gemini-3-pro":       "gemini",
		"GEMINI-3-PRO":       "gemini",
		"grok-4":             "xai",
		"mistral-large":      "mistral",
		"magistral-small":    "mistral",
		"some-gateway-model": "",
		"":                   "",
	}
	for in, want := range tests {
		if got := providerFromModelName(in); got != want {
			t.Errorf("providerFromModelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGeminiGroundingRegistersOnlyForGemini(t *testing.T) {
	isolate(t)

	hasGrounding := func(ag *Agent) bool {
		for _, tl := range ag.Tools() {
			if tl.Name() == agent.GroundingToolName {
				return true
			}
		}
		return false
	}

	gemini := newTestAgent(t, &fakeLLM{name: "gemini-3-pro", reply: "ok"})
	if !hasGrounding(gemini) {
		t.Error("a Gemini model did not get the grounding tool")
	}

	other := newTestAgent(t, &fakeLLM{name: "claude-sonnet-5", reply: "ok"})
	if hasGrounding(other) {
		t.Error("a non-Gemini model got the Gemini grounding tool")
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
		WithModel(&fakeLLM{name: "fake"}),
		WithWorkingDir(t.TempDir()),
		WithSessionDir(file),
		WithSubagents(false), WithLSP(LSPOff))
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
		WithModel(&fakeLLM{name: "fake", reply: "ok"}),
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
