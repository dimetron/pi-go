package pirpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	adkmodel "google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/agent"
)

// decodeLines parses NDJSON output into generic maps.
func decodeLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var got []map[string]any
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("output line is not JSON: %q: %v", line, err)
		}
		got = append(got, m)
	}
	return got
}

// runCommands feeds commands through a Server and returns the emitted objects.
func runCommands(t *testing.T, commands ...string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	s := NewServer(Config{
		SessionID: "sess-test",
		Model:     "test-model",
		In:        strings.NewReader(strings.Join(commands, "\n") + "\n"),
		Out:       &out,
	})
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	return decodeLines(t, out.String())
}

func TestDispatchRespondsToEveryCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		id      string
		success bool
	}{
		{"get_state", `{"type":"get_state","id":"i1"}`, "i1", true},
		{"get_available_models", `{"type":"get_available_models","id":"i2"}`, "i2", true},
		{"get_commands", `{"type":"get_commands","id":"i3"}`, "i3", true},
		{"get_messages", `{"type":"get_messages","id":"i4"}`, "i4", true},
		{"get_session_stats", `{"type":"get_session_stats","id":"i5"}`, "i5", true},
		{"set_thinking_level", `{"type":"set_thinking_level","id":"i6","level":"high"}`, "i6", true},
		{"unknown", `{"type":"nope","id":"i7"}`, "i7", false},
		{"export_html unsupported", `{"type":"export_html","id":"i8"}`, "i8", false},
		{"prompt without message", `{"type":"prompt","id":"i9"}`, "i9", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runCommands(t, tt.command)
			if len(got) != 1 {
				t.Fatalf("got %d objects, want 1: %v", len(got), got)
			}
			resp := got[0]
			if resp["type"] != "response" {
				t.Errorf("type = %v, want response", resp["type"])
			}
			if resp["id"] != tt.id {
				t.Errorf("id = %v, want %v", resp["id"], tt.id)
			}
			if resp["success"] != tt.success {
				t.Errorf("success = %v, want %v (error=%v)", resp["success"], tt.success, resp["error"])
			}
		})
	}
}

// pi-acp treats an empty model list as "unauthenticated" and refuses to create
// a session, so a non-empty catalog is a hard requirement of the integration.
func TestAvailableModelsIsNeverEmpty(t *testing.T) {
	data := availableModels()
	models, ok := data["models"].([]modelEntry)
	if !ok {
		t.Fatalf("models has type %T, want []modelEntry", data["models"])
	}
	if len(models) == 0 {
		t.Fatal("models is empty; pi-acp would reject session creation as unauthenticated")
	}
	for _, m := range models {
		if m.ID == "" || m.Provider == "" {
			t.Errorf("model missing id or provider: %+v", m)
		}
	}
}

func TestStateReportsSessionIdentity(t *testing.T) {
	s := NewServer(Config{SessionID: "sess-abc", Model: "m1"})
	st := s.state()
	if st["sessionId"] != "sess-abc" {
		t.Errorf("sessionId = %v, want sess-abc", st["sessionId"])
	}
	if _, ok := st["tokens"].(map[string]any); !ok {
		t.Errorf("tokens block missing, got %T", st["tokens"])
	}
}

// tool_execution_start and _end must carry the same id or pi-acp cannot pair
// them into one tool card.
func TestToolCallIDPrefersModelIDAndIsStable(t *testing.T) {
	s := NewServer(Config{})
	if got := s.toolCallID("call_123", "read"); got != "call_123" {
		t.Errorf("toolCallID with model id = %q, want call_123", got)
	}

	first := s.toolCallID("", "bash")
	second := s.toolCallID("", "bash")
	if first == second {
		t.Errorf("synthesized ids collided: %q", first)
	}
	if !strings.HasPrefix(first, "bash-") {
		t.Errorf("synthesized id = %q, want bash- prefix", first)
	}
}

func TestMalformedLineDoesNotKillTheServer(t *testing.T) {
	var out bytes.Buffer
	s := NewServer(Config{
		SessionID: "sess-test",
		In:        strings.NewReader("{not json}\n" + `{"type":"get_state","id":"after"}` + "\n"),
		Out:       &out,
	})
	if err := s.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := decodeLines(t, out.String())
	if len(got) == 0 {
		t.Fatal("no output")
	}
	last := got[len(got)-1]
	if last["id"] != "after" || last["success"] != true {
		t.Errorf("server did not recover from malformed input: %v", last)
	}
}

// A client that believes it switched models but did not is worse than one
// told the switch failed, so every set_model failure must surface as an error
// response rather than a cosmetic success.
func TestSetModelFailuresAreReportedNotSwallowed(t *testing.T) {
	okSwitcher := func(context.Context, string, string) (adkmodel.LLM, string, string, error) {
		return nil, "m", "p", nil
	}

	tests := []struct {
		name    string
		server  *Server
		modelID string
		wantErr string
	}{
		{
			name:    "empty model id",
			server:  NewServer(Config{Agent: &agent.Agent{}, ModelSwitcher: okSwitcher}),
			modelID: "",
			wantErr: "modelId is required",
		},
		{
			name:    "no switcher configured",
			server:  NewServer(Config{Agent: &agent.Agent{}}),
			modelID: "gpt-5.6-luna",
			wantErr: "unavailable",
		},
		{
			name: "switcher rejects the model",
			server: NewServer(Config{Agent: &agent.Agent{},
				ModelSwitcher: func(context.Context, string, string) (adkmodel.LLM, string, string, error) {
					return nil, "", "", errors.New("unknown model")
				}}),
			modelID: "not-a-real-model",
			wantErr: "unknown model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.server.setModel(context.Background(), tt.modelID, "")
			if err == nil {
				t.Fatal("setModel() = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// RebuildWithModel replaces the agent's LLM wholesale, which would swap the
// model out from under a response that is still streaming.
func TestSetModelRefusedDuringTurn(t *testing.T) {
	s := NewServer(Config{
		Agent: &agent.Agent{},
		ModelSwitcher: func(context.Context, string, string) (adkmodel.LLM, string, string, error) {
			t.Error("switcher called while a turn was running")
			return nil, "", "", nil
		},
	})
	s.cancel = func() {} // simulate an in-flight turn

	err := s.setModel(context.Background(), "gpt-5.6-luna", "")
	if err == nil || !strings.Contains(err.Error(), "while a turn is running") {
		t.Fatalf("setModel() error = %v, want a turn-in-progress refusal", err)
	}
}

func TestSetModelCommandReportsFailure(t *testing.T) {
	got := runCommands(t, `{"type":"set_model","id":"m1","provider":"openai","modelId":"gpt-5.6-luna"}`)
	if len(got) != 1 {
		t.Fatalf("got %d responses, want 1", len(got))
	}
	if got[0]["success"] != false {
		t.Errorf("success = %v, want false when no switcher is wired", got[0]["success"])
	}
	if got[0]["error"] == nil {
		t.Error("error field missing; the client would render a switch that never happened")
	}
}

func TestAbortWithoutRunningTurnIsSafe(t *testing.T) {
	got := runCommands(t, `{"type":"abort","id":"a1"}`)
	if len(got) != 1 || got[0]["success"] != true {
		t.Errorf("abort response = %v, want success", got)
	}
}
