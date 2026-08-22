package voicegemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/voice"
)

// Compile-time contract: Creator must satisfy voice.SessionCreator, so an
// interface drift is a build failure rather than a runtime surprise.
var _ voice.SessionCreator = (*Creator)(nil)

func TestVerify(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		model   string
		status  int
		body    string
		wantErr string
	}{
		{
			name:   "live model accepted",
			key:    "AIzaSyTestKeyLongEnough",
			status: http.StatusOK,
			body:   `{"supportedGenerationMethods":["bidiGenerateContent","countTokens"]}`,
		},
		{
			// The failure the whole check exists for: the model is real, the key
			// is fine, and every session would still die at the microphone.
			name:    "model exists but is not Live",
			key:     "AIzaSyTestKeyLongEnough",
			status:  http.StatusOK,
			body:    `{"supportedGenerationMethods":["generateContent"]}`,
			wantErr: "does not serve the Live API",
		},
		{
			name:    "no generation methods at all",
			key:     "AIzaSyTestKeyLongEnough",
			status:  http.StatusOK,
			body:    `{}`,
			wantErr: "no generation methods at all",
		},
		{
			name:    "key rejected",
			key:     "AIzaSyTestKeyLongEnough",
			status:  http.StatusForbidden,
			body:    `permission denied`,
			wantErr: "rejected (status 403)",
		},
		{
			name:    "model not found",
			key:     "AIzaSyTestKeyLongEnough",
			status:  http.StatusNotFound,
			body:    `no such model`,
			wantErr: "not found for this key",
		},
		{
			name:    "server error",
			key:     "AIzaSyTestKeyLongEnough",
			status:  http.StatusInternalServerError,
			body:    `boom`,
			wantErr: "status 500",
		},
		{
			name:    "missing key",
			key:     "",
			wantErr: "GEMINI_API_KEY is not set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("x-goog-api-key"); got != tt.key {
					t.Errorf("api key header = %q, want %q", got, tt.key)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			c := New(tt.key, WithModelsURL(srv.URL), WithHTTPClient(srv.Client()))
			if tt.model != "" {
				c.Model = tt.model
			}
			err := c.Verify(context.Background())
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Verify() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Verify() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Verify() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// A rejected key must never be reproduced in full — the error goes to a log and
// a terminal.
func TestVerifyMasksTheKey(t *testing.T) {
	const key = "AIzaSyVerySecretKeyValue123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(key, WithModelsURL(srv.URL), WithHTTPClient(srv.Client()))
	err := c.Verify(context.Background())
	if err == nil {
		t.Fatal("Verify() = nil, want error")
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("Verify() leaked the key: %v", err)
	}
	if !strings.Contains(err.Error(), "AIzaSy***") {
		t.Errorf("Verify() = %v, want a masked key", err)
	}
}

func TestMaskKey(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "***(len 0)"},
		{"short", "***(len 5)"},
		{"AIzaSyExactly16C", "AIzaSy***y16C(len 16)"},
	}
	for _, tt := range tests {
		if got := MaskKey(tt.in); got != tt.want {
			t.Errorf("MaskKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestWithModelSelection(t *testing.T) {
	c := New("key", WithModel("custom-pinned-model"))

	// An operator override stays selectable, or switching away from it would be
	// a one-way door.
	models := c.Models()
	if len(models) != len(LiveModels)+1 || models[0] != "custom-pinned-model" {
		t.Fatalf("Models() = %v, want the override first", models)
	}

	if got := c.WithModelSelection(""); got != c {
		t.Error("empty selection should return the same Creator")
	}
	if got := c.WithModelSelection("not-a-real-model"); got != c {
		t.Error("unknown selection should return the same Creator")
	}

	sel := c.WithModelSelection(DefaultModel)
	if sel == c {
		t.Fatal("a valid selection must return a copy, not mutate the shared Creator")
	}
	if sel.Model != DefaultModel {
		t.Errorf("selected model = %q, want %q", sel.Model, DefaultModel)
	}
	if c.Model != "custom-pinned-model" {
		t.Errorf("the shared Creator was mutated: %q", c.Model)
	}
}

func TestSetupMessage(t *testing.T) {
	c := New("key",
		WithInstructions("be brief"),
		WithTools([]Tool{{Name: "search", Description: "search the docs"}}),
	)
	raw, err := json.Marshal(c.SetupMessage())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got struct {
		Setup Setup `json:"setup"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Setup.Model != "models/"+DefaultModel {
		t.Errorf("model = %q, want the fully-qualified name", got.Setup.Model)
	}
	if got.Setup.GenerationConfig == nil || len(got.Setup.GenerationConfig.ResponseModalities) != 1 ||
		got.Setup.GenerationConfig.ResponseModalities[0] != "AUDIO" {
		t.Errorf("responseModalities = %+v, want [AUDIO]", got.Setup.GenerationConfig)
	}
	if got.Setup.InputTranscribe == nil || got.Setup.OutputTranscribe == nil {
		t.Error("both transcription directions must be requested")
	}
	if got.Setup.SystemInstruction == nil || got.Setup.SystemInstruction.Parts[0].Text != "be brief" {
		t.Errorf("systemInstruction = %+v", got.Setup.SystemInstruction)
	}
	if len(got.Setup.Tools) != 1 || len(got.Setup.Tools[0].FunctionDeclarations) != 1 {
		t.Errorf("tools = %+v, want one declaration", got.Setup.Tools)
	}
}

// InstructionsFunc wins over the static field, but only when it returns
// something — an empty return must not blank the configured persona.
func TestSetupMessageInstructionsFunc(t *testing.T) {
	c := New("key", WithInstructions("static"), WithInstructionsFunc(func() string { return "dynamic" }))
	setup := c.SetupMessage()["setup"].(Setup)
	if setup.SystemInstruction.Parts[0].Text != "dynamic" {
		t.Errorf("instructions = %q, want the func's value", setup.SystemInstruction.Parts[0].Text)
	}

	c = New("key", WithInstructions("static"), WithInstructionsFunc(func() string { return "" }))
	setup = c.SetupMessage()["setup"].(Setup)
	if setup.SystemInstruction.Parts[0].Text != "static" {
		t.Errorf("instructions = %q, want the static value", setup.SystemInstruction.Parts[0].Text)
	}
}

func TestCreateCarriesNoCredential(t *testing.T) {
	c := New("AIzaSySecretKeyValue1234")
	sess, err := c.Create(context.Background(), "thread")
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	raw, err := json.Marshal(sess.Realtime)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "AIzaSy") {
		t.Fatalf("Create() put a credential in the browser payload: %s", raw)
	}
	if sess.Realtime["transport"] != "gemini" || sess.Realtime["ws"] != "/api/voice/gemini/ws" {
		t.Errorf("Realtime = %v, want the relay descriptor", sess.Realtime)
	}
	if sess.ExpiresAt.IsZero() {
		t.Error("ExpiresAt must be set")
	}
}

func TestCreateWithoutKey(t *testing.T) {
	if _, err := New("").Create(context.Background(), "thread"); err == nil {
		t.Fatal("Create() = nil error, want a missing-key failure")
	}
}

func TestDialURLEscapesTheKey(t *testing.T) {
	c := New("key/with+special=chars", WithLiveURL("wss://example.test/live"))
	got := c.DialURL()
	if !strings.HasPrefix(got, "wss://example.test/live?key=") {
		t.Fatalf("DialURL() = %q", got)
	}
	if strings.Contains(got, "key/with+special=chars") {
		t.Errorf("DialURL() did not escape the key: %q", got)
	}
}

func TestRealtimeAudioMessage(t *testing.T) {
	msg := RealtimeAudioMessage("AAAA")
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		RealtimeInput struct {
			Audio struct {
				MimeType string `json:"mimeType"`
				Data     string `json:"data"`
			} `json:"audio"`
		} `json:"realtimeInput"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.RealtimeInput.Audio.MimeType != InputMimeType {
		t.Errorf("mimeType = %q, want %q", got.RealtimeInput.Audio.MimeType, InputMimeType)
	}
	if got.RealtimeInput.Audio.Data != "AAAA" {
		t.Errorf("data = %q", got.RealtimeInput.Audio.Data)
	}
}

func TestAudioParts(t *testing.T) {
	var nilContent *ServerContent
	if got := nilContent.AudioParts(); got != nil {
		t.Errorf("nil ServerContent AudioParts() = %v, want nil", got)
	}

	sc := &ServerContent{ModelTurn: &Content{Parts: []Part{
		{Text: "thinking out loud"},
		{InlineData: &InlineData{MimeType: "audio/pcm;rate=24000", Data: "first"}},
		{InlineData: &InlineData{MimeType: "image/png", Data: "not audio"}},
		{InlineData: &InlineData{MimeType: "audio/pcm", Data: "second"}},
	}}}
	got := sc.AudioParts()
	want := []string{"first", "second"}
	if len(got) != len(want) {
		t.Fatalf("AudioParts() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("AudioParts()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestServerMessageDecoding(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		check func(*testing.T, ServerMessage)
	}{
		{
			name: "setupComplete",
			raw:  `{"setupComplete":{}}`,
			check: func(t *testing.T, m ServerMessage) {
				if m.SetupComplete == nil {
					t.Error("SetupComplete = nil")
				}
			},
		},
		{
			name: "transcriptions",
			raw:  `{"serverContent":{"inputTranscription":{"text":"hello"},"outputTranscription":{"text":"hi"}}}`,
			check: func(t *testing.T, m ServerMessage) {
				if m.ServerContent.InputTranscr.Text != "hello" || m.ServerContent.OutputTranscr.Text != "hi" {
					t.Errorf("transcriptions = %+v", m.ServerContent)
				}
			},
		},
		{
			name: "toolCall",
			raw:  `{"toolCall":{"functionCalls":[{"id":"c1","name":"search","args":{"q":"x"}}]}}`,
			check: func(t *testing.T, m ServerMessage) {
				if len(m.ToolCall.FunctionCalls) != 1 || m.ToolCall.FunctionCalls[0].Name != "search" {
					t.Errorf("toolCall = %+v", m.ToolCall)
				}
			},
		},
		{
			name: "goAway",
			raw:  `{"goAway":{"timeLeft":"10s"}}`,
			check: func(t *testing.T, m ServerMessage) {
				if m.GoAway.TimeLeft != "10s" {
					t.Errorf("goAway = %+v", m.GoAway)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m ServerMessage
			if err := json.Unmarshal([]byte(tt.raw), &m); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			tt.check(t, m)
		})
	}
}

func TestToolResponseMessage(t *testing.T) {
	msg := ToolResponseMessage([]FunctionResponse{
		{ID: "call-1", Name: "search", Response: map[string]any{"output": "ok"}},
	})
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		ToolResponse struct {
			FunctionResponses []FunctionResponse `json:"functionResponses"`
		} `json:"toolResponse"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.ToolResponse.FunctionResponses) != 1 {
		t.Fatalf("functionResponses = %+v, want one", got.ToolResponse.FunctionResponses)
	}
	// The id is what matches the answer to the call; dropping it strands the turn.
	fr := got.ToolResponse.FunctionResponses[0]
	if fr.ID != "call-1" || fr.Name != "search" || fr.Response["output"] != "ok" {
		t.Errorf("functionResponse = %+v", fr)
	}
}
