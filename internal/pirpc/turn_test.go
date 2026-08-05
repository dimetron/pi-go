package pirpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	adkagent "google.golang.org/adk/v2/agent"
	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/logger"
)

// scriptedLLM is an adkmodel.LLM whose reply depends on which invocation it is.
// A turn that calls a tool reaches the model twice — once to request the call,
// once with the result — so a single canned response cannot drive those paths.
type scriptedLLM struct {
	mu     sync.Mutex
	calls  int
	script func(call int) ([]*adkmodel.LLMResponse, error)
}

func (m *scriptedLLM) Name() string { return "scripted-model" }

func (m *scriptedLLM) GenerateContent(_ context.Context, _ *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	m.mu.Lock()
	call := m.calls
	m.calls++
	m.mu.Unlock()

	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		resps, err := m.script(call)
		if err != nil {
			yield(nil, err)
			return
		}
		for _, r := range resps {
			if !yield(r, nil) {
				return
			}
		}
	}
}

// textResponse builds a plain assistant reply.
func textResponse(text string) *adkmodel.LLMResponse {
	return &adkmodel.LLMResponse{Content: genai.NewContentFromText(text, genai.RoleModel)}
}

// partsResponse builds a reply from explicit parts, for roles and part kinds
// NewContentFromText cannot express.
func partsResponse(role string, parts ...*genai.Part) *adkmodel.LLMResponse {
	return &adkmodel.LLMResponse{Content: &genai.Content{Role: role, Parts: parts}}
}

// syncBuf is an io.Writer safe for the turn goroutine and the read loop to
// share, which is exactly the contention Server.writeMu exists to handle.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncBuf) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncBuf) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// waitForType blocks until an emitted object carries the given "type", so tests
// synchronize on the protocol rather than on a sleep.
func (w *syncBuf) waitForType(t *testing.T, typ string) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if out := w.String(); strings.Contains(out, `"`+typ+`"`) {
			got := decodeLines(t, out)
			for _, ev := range got {
				if ev["type"] == typ {
					return got
				}
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for a %q event; got:\n%s", typ, w.String())
	return nil
}

// echoArgs is the argument struct of the test tool.
type echoArgs struct {
	Text string `json:"text"`
}

// echoResult is what the test tool returns.
type echoResult struct {
	Echoed string `json:"echoed"`
}

// echoTool is a tool with no side effects, so a turn can exercise the
// tool_execution_start/_end path without touching the filesystem or network.
func echoTool(t *testing.T) tool.Tool {
	t.Helper()
	tl, err := functiontool.New(functiontool.Config{
		Name:        "echo",
		Description: "Echoes the text back.",
	}, func(_ adkagent.Context, a echoArgs) (echoResult, error) {
		return echoResult{Echoed: a.Text}, nil
	})
	if err != nil {
		t.Fatalf("building echo tool: %v", err)
	}
	return tl
}

// newTurnServer wires a Server around a real Agent driven by llm, which is the
// only way to reach runTurn: the streaming path runs through the ADK runner.
func newTurnServer(t *testing.T, llm adkmodel.LLM, tools ...tool.Tool) (*Server, *syncBuf) {
	t.Helper()

	a, err := agent.New(agent.Config{
		Model:          llm,
		Tools:          tools,
		Instruction:    "You are a test agent.",
		SessionService: session.InMemoryService(),
	})
	if err != nil {
		t.Fatalf("agent.New() error = %v", err)
	}

	sessionID, _, err := a.CreateSession(context.Background())
	if err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	out := &syncBuf{}
	return NewServer(Config{
		Agent:     a,
		SessionID: sessionID,
		Model:     "scripted-model",
		Out:       out,
	}), out
}

// eventTypes lists the "type" field of every emitted object, in order.
func eventTypes(events []map[string]any) []string {
	types := make([]string, 0, len(events))
	for _, ev := range events {
		s, _ := ev["type"].(string)
		types = append(types, s)
	}
	return types
}

// assistantDeltas collects the streamed chunks of one kind from message_update
// events.
func assistantDeltas(events []map[string]any, kind string) []string {
	var deltas []string
	for _, ev := range events {
		if ev["type"] != "message_update" {
			continue
		}
		ame, ok := ev["assistantMessageEvent"].(map[string]any)
		if !ok || ame["type"] != kind {
			continue
		}
		d, _ := ame["delta"].(string)
		deltas = append(deltas, d)
	}
	return deltas
}

// pi-acp resolves the ACP session/prompt request on agent_settled and on
// nothing else, so a turn that omits it hangs the client forever.
func TestRunTurnStreamsTextAndSettles(t *testing.T) {
	llm := &scriptedLLM{script: func(int) ([]*adkmodel.LLMResponse, error) {
		return []*adkmodel.LLMResponse{textResponse("Hello there")}, nil
	}}
	s, out := newTurnServer(t, llm)

	s.runTurn(context.Background(), "hi")

	events := decodeLines(t, out.String())
	types := eventTypes(events)
	if len(types) < 2 {
		t.Fatalf("got %v, want at least a start and a settle", types)
	}
	if types[0] != "agent_start" {
		t.Errorf("first event = %q, want agent_start", types[0])
	}
	if got := types[len(types)-1]; got != "agent_settled" {
		t.Errorf("last event = %q, want agent_settled", got)
	}
	if got := types[len(types)-2]; got != "agent_end" {
		t.Errorf("second-to-last event = %q, want agent_end", got)
	}

	deltas := assistantDeltas(events, "text_delta")
	if len(deltas) == 0 {
		t.Fatal("no text_delta emitted; the client would render an empty reply")
	}
	if joined := strings.Join(deltas, ""); !strings.Contains(joined, "Hello there") {
		t.Errorf("text deltas = %q, want them to contain the model reply", joined)
	}
}

func TestRunTurnEmitsThinkingSeparatelyFromText(t *testing.T) {
	llm := &scriptedLLM{script: func(int) ([]*adkmodel.LLMResponse, error) {
		return []*adkmodel.LLMResponse{
			partsResponse("thinking", &genai.Part{Text: "weighing options"}),
			textResponse("the answer"),
		}, nil
	}}
	s, out := newTurnServer(t, llm)

	s.runTurn(context.Background(), "think first")

	events := decodeLines(t, out.String())
	thinking := strings.Join(assistantDeltas(events, "thinking_delta"), "")
	text := strings.Join(assistantDeltas(events, "text_delta"), "")

	if !strings.Contains(thinking, "weighing options") {
		t.Errorf("thinking_delta = %q, want the reasoning text", thinking)
	}
	if strings.Contains(text, "weighing options") {
		t.Errorf("reasoning leaked into text_delta = %q", text)
	}
	if !strings.Contains(text, "the answer") {
		t.Errorf("text_delta = %q, want the assistant reply", text)
	}
}

// tool_execution_start and _end must carry the same toolCallId or pi-acp
// cannot pair them into one tool card.
func TestRunTurnPairsToolExecutionEvents(t *testing.T) {
	llm := &scriptedLLM{script: func(call int) ([]*adkmodel.LLMResponse, error) {
		if call == 0 {
			return []*adkmodel.LLMResponse{partsResponse(string(genai.RoleModel), &genai.Part{
				FunctionCall: &genai.FunctionCall{
					ID:   "call_1",
					Name: "echo",
					Args: map[string]any{"text": "ping"},
				},
			})}, nil
		}
		return []*adkmodel.LLMResponse{textResponse("done")}, nil
	}}
	s, out := newTurnServer(t, llm, echoTool(t))

	s.runTurn(context.Background(), "use the tool")

	events := decodeLines(t, out.String())
	var startID, endID string
	var startName string
	for _, ev := range events {
		switch ev["type"] {
		case "tool_execution_start":
			startID, _ = ev["toolCallId"].(string)
			startName, _ = ev["toolName"].(string)
		case "tool_execution_end":
			endID, _ = ev["toolCallId"].(string)
		}
	}

	if startID == "" {
		t.Fatalf("no tool_execution_start emitted; events = %v", eventTypes(events))
	}
	if startName != "echo" {
		t.Errorf("toolName = %q, want echo", startName)
	}
	if endID == "" {
		t.Fatalf("no tool_execution_end emitted; the tool card would never close")
	}
	if startID != endID {
		t.Errorf("toolCallId start = %q, end = %q; they must match to pair", startID, endID)
	}
}

// pi has no error event pi-acp renders, so a failed run has to reach the user
// as assistant text — and the turn must still settle.
func TestRunTurnReportsErrorsAsAssistantText(t *testing.T) {
	llm := &scriptedLLM{script: func(int) ([]*adkmodel.LLMResponse, error) {
		return nil, errors.New("model exploded")
	}}
	s, out := newTurnServer(t, llm)

	s.runTurn(context.Background(), "boom")

	events := decodeLines(t, out.String())
	text := strings.Join(assistantDeltas(events, "text_delta"), "")
	if !strings.Contains(text, "model exploded") {
		t.Errorf("text deltas = %q, want the failure surfaced to the user", text)
	}
	if got := eventTypes(events); got[len(got)-1] != "agent_settled" {
		t.Errorf("last event = %q, want agent_settled even after a failure", got[len(got)-1])
	}
}

// An aborted turn must not report the cancellation as a model error, but it
// must still settle or the client waits forever.
func TestRunTurnCanceledContextSettlesWithoutError(t *testing.T) {
	llm := &scriptedLLM{script: func(int) ([]*adkmodel.LLMResponse, error) {
		return nil, context.Canceled
	}}
	s, out := newTurnServer(t, llm)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.runTurn(ctx, "never mind")

	events := decodeLines(t, out.String())
	types := eventTypes(events)
	if got := types[len(types)-1]; got != "agent_settled" {
		t.Errorf("last event = %q, want agent_settled after an abort", got)
	}
	if text := strings.Join(assistantDeltas(events, "text_delta"), ""); strings.Contains(text, "Error:") {
		t.Errorf("abort rendered as a model error: %q", text)
	}
}

// runTurn clears s.cancel on the way out; leaving it set would make every
// later set_model believe a turn was still running.
func TestRunTurnClearsCancelOnExit(t *testing.T) {
	llm := &scriptedLLM{script: func(int) ([]*adkmodel.LLMResponse, error) {
		return []*adkmodel.LLMResponse{textResponse("ok")}, nil
	}}
	s, _ := newTurnServer(t, llm)

	s.runTurn(context.Background(), "hi")

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		t.Error("s.cancel still set after the turn; set_model would refuse forever")
	}
}

// The prompt command is acknowledged before the turn runs, so abort stays
// readable while the model streams.
func TestPromptCommandAcksThenStreamsTurn(t *testing.T) {
	llm := &scriptedLLM{script: func(int) ([]*adkmodel.LLMResponse, error) {
		return []*adkmodel.LLMResponse{textResponse("streamed reply")}, nil
	}}
	s, out := newTurnServer(t, llm)
	s.in = strings.NewReader(`{"type":"prompt","id":"p1","message":"hello"}` + "\n")

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events := out.waitForType(t, "agent_settled")

	if events[0]["type"] != "response" || events[0]["id"] != "p1" {
		t.Errorf("first object = %v, want the prompt acknowledgement", events[0])
	}
	if events[0]["success"] != true {
		t.Errorf("prompt ack success = %v, want true", events[0]["success"])
	}
	if text := strings.Join(assistantDeltas(events, "text_delta"), ""); !strings.Contains(text, "streamed reply") {
		t.Errorf("text deltas = %q, want the model reply", text)
	}
}

// blockingLLM stalls until the turn's context is canceled, standing in for a
// model that is still streaming when the user hits abort.
type blockingLLM struct{ entered chan struct{} }

func (m *blockingLLM) Name() string { return "blocking-model" }

func (m *blockingLLM) GenerateContent(ctx context.Context, _ *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		select {
		case m.entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		yield(nil, ctx.Err())
	}
}

// abort has to cancel the in-flight turn, not merely answer politely — and the
// canceled turn must still settle or pi-acp waits forever.
func TestAbortCancelsRunningTurn(t *testing.T) {
	llm := &blockingLLM{entered: make(chan struct{}, 1)}
	s, out := newTurnServer(t, llm)

	ctx := context.Background()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runTurn(ctx, "long one")
	}()

	select {
	case <-llm.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("the model was never reached; the turn did not start")
	}

	s.dispatch(ctx, command{Type: "abort", ID: "a1"})

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("abort did not cancel the running turn")
	}

	events := decodeLines(t, out.String())
	types := eventTypes(events)
	if got := types[len(types)-1]; got != "agent_settled" {
		t.Errorf("last event = %q, want agent_settled after abort", got)
	}
	// The cancellation is the user's doing, not a model failure.
	if text := strings.Join(assistantDeltas(events, "text_delta"), ""); strings.Contains(text, "Error:") {
		t.Errorf("abort rendered as a model error: %q", text)
	}
}

// A successful switch has to be visible in get_state, since that is what the
// client renders as the active model.
func TestSetModelSuccessUpdatesState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	log, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New() error = %v", err)
	}

	llm := &scriptedLLM{script: func(int) ([]*adkmodel.LLMResponse, error) {
		return []*adkmodel.LLMResponse{textResponse("ok")}, nil
	}}
	s, _ := newTurnServer(t, llm)
	s.log = log

	var gotHint string
	s.modelSwitcher = func(_ context.Context, name, providerHint string) (adkmodel.LLM, string, string, error) {
		gotHint = providerHint
		return &scriptedLLM{script: func(int) ([]*adkmodel.LLMResponse, error) {
			return []*adkmodel.LLMResponse{textResponse("new model")}, nil
		}}, name, "openai", nil
	}

	if err := s.setModel(context.Background(), "gpt-5.6-luna", "openai"); err != nil {
		t.Fatalf("setModel() error = %v", err)
	}

	// The hint is load-bearing: without it pi-go infers the provider from the
	// default role and can route an OpenAI model through Ollama.
	if gotHint != "openai" {
		t.Errorf("providerHint = %q, want it forwarded to the switcher", gotHint)
	}

	st := s.state()
	if st["model"] != "gpt-5.6-luna" {
		t.Errorf("state model = %v, want gpt-5.6-luna", st["model"])
	}
	if st["provider"] != "openai" {
		t.Errorf("state provider = %v, want openai", st["provider"])
	}

	// The switch is invisible in the RPC stream, so the session log is the only
	// place a later debugging pass can see which model actually ran.
	if err := log.Close(); err != nil {
		t.Fatalf("closing log: %v", err)
	}
	contents, err := os.ReadFile(log.Path())
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	if !bytes.Contains(contents, []byte("switched model to gpt-5.6-luna")) {
		t.Errorf("session log does not record the switch:\n%s", contents)
	}
}

func TestSetModelCommandSuccessReportsNewState(t *testing.T) {
	llm := &scriptedLLM{script: func(int) ([]*adkmodel.LLMResponse, error) {
		return []*adkmodel.LLMResponse{textResponse("ok")}, nil
	}}
	s, out := newTurnServer(t, llm)
	s.modelSwitcher = func(_ context.Context, name, _ string) (adkmodel.LLM, string, string, error) {
		return &scriptedLLM{script: func(int) ([]*adkmodel.LLMResponse, error) {
			return []*adkmodel.LLMResponse{textResponse("x")}, nil
		}}, name, "anthropic", nil
	}
	s.in = strings.NewReader(`{"type":"set_model","id":"m1","modelId":"claude-x","provider":"anthropic"}` + "\n")

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	events := decodeLines(t, out.String())
	if len(events) != 1 {
		t.Fatalf("got %d responses, want 1: %v", len(events), events)
	}
	if events[0]["success"] != true {
		t.Fatalf("success = %v, want true (error=%v)", events[0]["success"], events[0]["error"])
	}
	data, ok := events[0]["data"].(map[string]any)
	if !ok {
		t.Fatalf("data has type %T, want the state map", events[0]["data"])
	}
	if data["model"] != "claude-x" {
		t.Errorf("state model = %v, want claude-x", data["model"])
	}
}

// A switcher that succeeds but whose agent rebuild fails must still report a
// failure, or the client shows a model that is not actually in use.
func TestSetModelRebuildFailureIsReported(t *testing.T) {
	s := NewServer(Config{
		Agent: &agent.Agent{}, // zero agent: RebuildWithModel has no runner to rebuild
		ModelSwitcher: func(_ context.Context, name, _ string) (adkmodel.LLM, string, string, error) {
			return nil, name, "openai", nil
		},
	})

	err := s.setModel(context.Background(), "gpt-5.6-luna", "openai")
	if err == nil {
		t.Fatal("setModel() = nil, want an error when the rebuild cannot succeed")
	}
	if !strings.Contains(err.Error(), "rebuilding agent") {
		t.Errorf("error = %q, want it to name the rebuild failure", err)
	}
}

// The log is optional, but when present every channel of a turn should reach
// it — that log is the only record of a headless RPC session.
func TestRunTurnWritesToTheSessionLog(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	log, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New() error = %v", err)
	}

	llm := &scriptedLLM{script: func(call int) ([]*adkmodel.LLMResponse, error) {
		if call == 0 {
			return []*adkmodel.LLMResponse{
				partsResponse("thinking", &genai.Part{Text: "pondering"}),
				partsResponse(string(genai.RoleModel), &genai.Part{FunctionCall: &genai.FunctionCall{
					ID: "call_1", Name: "echo", Args: map[string]any{"text": "ping"},
				}}),
			}, nil
		}
		return []*adkmodel.LLMResponse{textResponse("all done")}, nil
	}}
	s, _ := newTurnServer(t, llm, echoTool(t))
	s.log = log

	s.runTurn(context.Background(), "log this")

	// Streamed assistant text is coalesced in memory and only flushed on Close,
	// so the log is incomplete until then.
	if err := log.Close(); err != nil {
		t.Fatalf("closing log: %v", err)
	}

	contents, err := os.ReadFile(log.Path())
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	for _, want := range []string{"log this", "pondering", "echo", "all done"} {
		if !bytes.Contains(contents, []byte(want)) {
			t.Errorf("log is missing %q; got:\n%s", want, contents)
		}
	}
}

func TestEmitErrorIsLoggedAndSurfaced(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	log, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New() error = %v", err)
	}

	out := &syncBuf{}
	s := NewServer(Config{Out: out, Log: log})
	s.emitError(errors.New("upstream refused"))

	if text := strings.Join(assistantDeltas(decodeLines(t, out.String()), "text_delta"), ""); !strings.Contains(text, "upstream refused") {
		t.Errorf("text delta = %q, want the error surfaced as assistant text", text)
	}

	if err := log.Close(); err != nil {
		t.Fatalf("closing log: %v", err)
	}

	contents, err := os.ReadFile(log.Path())
	if err != nil {
		t.Fatalf("reading log: %v", err)
	}
	if !bytes.Contains(contents, []byte("upstream refused")) {
		t.Errorf("error missing from the session log:\n%s", contents)
	}
}

// pi-acp persists sessionFile into its session map, so advertising a path that
// does not exist makes a later session/load unresolvable.
func TestStateAdvertisesSessionFileOnlyWhenItExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	s := NewServer(Config{SessionID: "sess-file", Model: "m"})
	if _, ok := s.state()["sessionFile"]; ok {
		t.Error("sessionFile advertised for a session with no file on disk")
	}

	dir := filepath.Join(home, ".pi-go", "sessions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("creating session dir: %v", err)
	}
	path := filepath.Join(dir, "sess-file.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("writing session file: %v", err)
	}

	if got := s.state()["sessionFile"]; got != path {
		t.Errorf("sessionFile = %v, want %s", got, path)
	}
}

// Run must stop when the context is canceled, even with input still pending,
// or a shutdown would block on a client that keeps writing.
func TestRunStopsOnCanceledContext(t *testing.T) {
	out := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	s := NewServer(Config{
		SessionID: "sess",
		In:        strings.NewReader(strings.Repeat(`{"type":"get_state","id":"x"}`+"\n", 5)),
		Out:       out,
	})
	if err := s.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if out.String() != "" {
		t.Errorf("Run() served commands after cancellation: %q", out.String())
	}
}

// errReader fails partway through, standing in for a broken stdin.
type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestRunReportsReadFailures(t *testing.T) {
	s := NewServer(Config{
		In:  errReader{err: errors.New("stdin died")},
		Out: &syncBuf{},
	})
	err := s.Run(context.Background())
	if err == nil {
		t.Fatal("Run() = nil, want the read failure reported")
	}
	if !strings.Contains(err.Error(), "stdin died") {
		t.Errorf("error = %q, want it to wrap the read failure", err)
	}
}

// A line larger than bufio.Scanner's 64KiB default is ordinary here: prompts
// carry whole files.
func TestRunAcceptsPromptsLargerThanTheDefaultScannerBuffer(t *testing.T) {
	llm := &scriptedLLM{script: func(int) ([]*adkmodel.LLMResponse, error) {
		return []*adkmodel.LLMResponse{textResponse("got it")}, nil
	}}
	s, out := newTurnServer(t, llm)

	big := strings.Repeat("x", 256<<10)
	s.in = strings.NewReader(fmt.Sprintf(`{"type":"prompt","id":"big","message":%q}`+"\n", big))

	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events := out.waitForType(t, "agent_settled")
	if events[0]["id"] != "big" || events[0]["success"] != true {
		t.Errorf("large prompt was rejected: %v", events[0])
	}
}

func TestRunSkipsBlankLines(t *testing.T) {
	out := &syncBuf{}
	s := NewServer(Config{
		SessionID: "sess",
		In:        strings.NewReader("\n   \n" + `{"type":"get_state","id":"only"}` + "\n\n"),
		Out:       out,
	})
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	events := decodeLines(t, out.String())
	if len(events) != 1 {
		t.Fatalf("got %d responses, want 1 (blank lines must not be answered): %v", len(events), events)
	}
}

// Interleaved NDJSON would be unparseable, so emit has to serialize writes
// across the turn goroutine and the read loop.
func TestEmitSerializesConcurrentWrites(t *testing.T) {
	out := &syncBuf{}
	s := NewServer(Config{Out: out})

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.emit(map[string]any{"type": "message_update", "n": i})
		}()
	}
	wg.Wait()

	events := decodeLines(t, out.String()) // fails the test if any line is not valid JSON
	if len(events) != 50 {
		t.Errorf("got %d lines, want 50", len(events))
	}
}

// A value json.Marshal rejects must not produce a truncated or empty line that
// would desynchronize the client's parser.
func TestEmitDropsUnmarshalableValues(t *testing.T) {
	out := &syncBuf{}
	s := NewServer(Config{Out: out})

	s.emit(make(chan int))

	if out.String() != "" {
		t.Errorf("emit wrote %q for an unmarshalable value, want nothing", out.String())
	}
}

// reply must never claim success alongside an error; the client reads only one
// of the two fields.
func TestReplyMarksErroredResponsesUnsuccessful(t *testing.T) {
	out := &syncBuf{}
	s := NewServer(Config{Out: out})

	s.reply(response{ID: "r1", Command: "prompt", Success: true, Error: "it failed"})

	events := decodeLines(t, out.String())
	if len(events) != 1 {
		t.Fatalf("got %d responses, want 1", len(events))
	}
	if events[0]["success"] != false {
		t.Errorf("success = %v, want false when an error is set", events[0]["success"])
	}
	if events[0]["type"] != "response" {
		t.Errorf("type = %v, want response", events[0]["type"])
	}
}

// These commands exist so the adapter's slash menu does not error; each must
// still answer with the current state.
func TestAcceptedNoOpCommandsReturnState(t *testing.T) {
	for _, cmd := range []string{
		"set_follow_up_mode", "set_steering_mode", "set_auto_compaction",
		"set_session_name", "switch_session", "compact",
	} {
		t.Run(cmd, func(t *testing.T) {
			got := runCommands(t, `{"type":"`+cmd+`","id":"c1"}`)
			if len(got) != 1 {
				t.Fatalf("got %d responses, want 1", len(got))
			}
			if got[0]["success"] != true {
				t.Errorf("success = %v, want true (error=%v)", got[0]["success"], got[0]["error"])
			}
			if _, ok := got[0]["data"].(map[string]any); !ok {
				t.Errorf("data has type %T, want the state map", got[0]["data"])
			}
		})
	}
}
