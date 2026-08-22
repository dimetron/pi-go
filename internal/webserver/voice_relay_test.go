//go:build !windows

package webserver

// Relay tests drive the real handler against a fake Gemini Live server.
//
// Only the provider boundary is faked: the WebSocket upgrade, the session
// store, both audio pumps, the write serialization and the event translation
// are the production code. That is what makes these worth running — a test that
// mocked the relay itself would prove only that the mock returns what it was
// told to.

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dimetron/pi-go/internal/voicegemini"
)

// fakeProvider is a stand-in for the Gemini Live endpoint. It records every
// client message and plays a scripted reply once setup arrives.
type fakeProvider struct {
	srv *httptest.Server

	mu       sync.Mutex
	received []map[string]any

	// script runs after the relay's setup message is read; it writes whatever
	// server envelopes the test needs.
	script func(t *testing.T, conn *websocket.Conn)

	// closeAfterSetup, when set, closes the connection with this code and text
	// instead of running the script.
	closeAfterSetup *websocket.CloseError
}

func newFakeProvider(t *testing.T, script func(*testing.T, *websocket.Conn)) *fakeProvider {
	t.Helper()
	fp := &fakeProvider{script: script}
	up := &websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	fp.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		// The relay always opens with setup.
		var setup map[string]any
		if err := conn.ReadJSON(&setup); err != nil {
			return
		}
		fp.record(setup)

		if fp.closeAfterSetup != nil {
			_ = conn.WriteControl(websocket.CloseMessage,
				websocket.FormatCloseMessage(fp.closeAfterSetup.Code, fp.closeAfterSetup.Text),
				time.Now().Add(time.Second))
			return
		}

		// Keep reading client messages (mic audio, tool responses) in the
		// background so the script can assert on them.
		go func() {
			for {
				var m map[string]any
				if err := conn.ReadJSON(&m); err != nil {
					return
				}
				fp.record(m)
			}
		}()

		if fp.script != nil {
			fp.script(t, conn)
		}
	}))
	t.Cleanup(fp.srv.Close)
	return fp
}

func (fp *fakeProvider) record(m map[string]any) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	fp.received = append(fp.received, m)
}

// messages returns a snapshot of what the relay sent the provider.
func (fp *fakeProvider) messages() []map[string]any {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return append([]map[string]any(nil), fp.received...)
}

// waitFor polls for a client message with the given top-level key.
func (fp *fakeProvider) waitFor(t *testing.T, key string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, m := range fp.messages() {
			if v, ok := m[key]; ok {
				sub, _ := v.(map[string]any)
				return sub
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no %q message reached the provider; got %v", key, fp.messages())
	return nil
}

func (fp *fakeProvider) wsURL() string {
	return "ws" + strings.TrimPrefix(fp.srv.URL, "http")
}

// relayHarness wires a pi web server whose provider is fp, and returns a
// browser-side connection to a live relay.
type relayHarness struct {
	server  *ServerV2
	http    *httptest.Server
	browser *websocket.Conn
	session string
}

func newRelayHarness(t *testing.T, fp *fakeProvider) *relayHarness {
	t.Helper()
	return newRelayHarnessOn(t, fp, "")
}

// newRelayHarnessOn is newRelayHarness with the voice session bound to a
// terminal id, which is what the agent tools drive.
func newRelayHarnessOn(t *testing.T, fp *fakeProvider, terminal string) *relayHarness {
	t.Helper()

	s := NewServerV2(Config{Addr: "127.0.0.1:0"})
	s.voiceGemini = voicegemini.New("AIzaSyTestKeyLongEnough", voicegemini.WithLiveURL(fp.wsURL()))

	mux := http.NewServeMux()
	s.setupRoutes(mux)
	httpSrv := httptest.NewServer(mux)
	t.Cleanup(httpSrv.Close)

	// Voice endpoints are paired-only, so the harness pairs before it can reach
	// any of them — the same order the browser follows.
	token := voiceToken(t, s)
	body, _ := json.Marshal(map[string]string{"terminal": terminal})
	res, err := httpSrv.Client().Post(httpSrv.URL+"/api/voice/sessions?token="+token, "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer res.Body.Close()
	var created struct {
		ID       string         `json:"id"`
		Realtime map[string]any `json:"realtime"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/api/voice/gemini/ws?token=" + token + "&session=" + created.ID
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	return &relayHarness{server: s, http: httpSrv, browser: conn, session: created.ID}
}

// nextEvent reads the next JSON event, skipping binary audio frames.
func (h *relayHarness) nextEvent(t *testing.T) map[string]any {
	t.Helper()
	for {
		kind, data, err := h.browser.ReadMessage()
		if err != nil {
			t.Fatalf("reading browser event: %v", err)
		}
		if kind != websocket.TextMessage {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("decoding browser event %s: %v", data, err)
		}
		return ev
	}
}

// nextBinary reads the next binary frame, skipping JSON events.
func (h *relayHarness) nextBinary(t *testing.T) []byte {
	t.Helper()
	for {
		kind, data, err := h.browser.ReadMessage()
		if err != nil {
			t.Fatalf("reading browser audio: %v", err)
		}
		if kind == websocket.BinaryMessage {
			return data
		}
	}
}

func TestRelayForwardsSetupCompleteAsReady(t *testing.T) {
	fp := newFakeProvider(t, func(t *testing.T, conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		time.Sleep(200 * time.Millisecond)
	})
	h := newRelayHarness(t, fp)

	if ev := h.nextEvent(t); ev["type"] != "ready" {
		t.Fatalf("first event = %v, want ready", ev)
	}

	// The server owns setup: model, both transcription directions, audio.
	setup := fp.waitFor(t, "setup")
	if got := setup["model"]; got != "models/"+voicegemini.DefaultModel {
		t.Errorf("setup model = %v, want the fully-qualified default", got)
	}
	if _, ok := setup["inputAudioTranscription"]; !ok {
		t.Error("setup did not request input transcription")
	}
	if _, ok := setup["outputAudioTranscription"]; !ok {
		t.Error("setup did not request output transcription")
	}
}

func TestRelayForwardsAudioAndTranscripts(t *testing.T) {
	pcm := []byte{0x01, 0x02, 0x03, 0x04}
	fp := newFakeProvider(t, func(t *testing.T, conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		_ = conn.WriteJSON(map[string]any{"serverContent": map[string]any{
			"modelTurn": map[string]any{"parts": []map[string]any{
				{"inlineData": map[string]any{
					"mimeType": "audio/pcm;rate=24000",
					"data":     base64.StdEncoding.EncodeToString(pcm),
				}},
			}},
			"inputTranscription":  map[string]any{"text": "hello"},
			"outputTranscription": map[string]any{"text": "hi there"},
			"turnComplete":        true,
		}})
		time.Sleep(300 * time.Millisecond)
	})
	h := newRelayHarness(t, fp)

	if ev := h.nextEvent(t); ev["type"] != "ready" {
		t.Fatalf("first event = %v, want ready", ev)
	}

	// Output audio arrives decoded, as binary frames the page can play.
	if got := h.nextBinary(t); string(got) != string(pcm) {
		t.Errorf("audio frame = %v, want %v", got, pcm)
	}

	want := []struct{ typ, text string }{
		{"transcript_user_delta", "hello"},
		{"transcript_assistant_delta", "hi there"},
		{"turn_complete", ""},
	}
	for _, w := range want {
		ev := h.nextEvent(t)
		if ev["type"] != w.typ {
			t.Fatalf("event = %v, want type %s", ev, w.typ)
		}
		if w.text != "" && ev["text"] != w.text {
			t.Errorf("%s text = %v, want %q", w.typ, ev["text"], w.text)
		}
	}
}

// Barge-in: the page drops queued playback on this event, so it must survive
// the relay.
func TestRelayForwardsInterrupted(t *testing.T) {
	fp := newFakeProvider(t, func(t *testing.T, conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		_ = conn.WriteJSON(map[string]any{"serverContent": map[string]any{"interrupted": true}})
		time.Sleep(200 * time.Millisecond)
	})
	h := newRelayHarness(t, fp)

	_ = h.nextEvent(t) // ready
	if ev := h.nextEvent(t); ev["type"] != "interrupted" {
		t.Errorf("event = %v, want interrupted", ev)
	}
}

func TestRelayForwardsGoAwayAndToolCancel(t *testing.T) {
	fp := newFakeProvider(t, func(t *testing.T, conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		_ = conn.WriteJSON(map[string]any{"toolCallCancellation": map[string]any{"ids": []string{"c1", "c2"}}})
		_ = conn.WriteJSON(map[string]any{"goAway": map[string]any{"timeLeft": "10s"}})
		time.Sleep(300 * time.Millisecond)
	})
	h := newRelayHarness(t, fp)

	_ = h.nextEvent(t) // ready

	ev := h.nextEvent(t)
	if ev["type"] != "tool_cancel" {
		t.Fatalf("event = %v, want tool_cancel", ev)
	}
	ids, _ := ev["ids"].([]any)
	if len(ids) != 2 || ids[0] != "c1" {
		t.Errorf("tool_cancel ids = %v, want [c1 c2]", ev["ids"])
	}

	if ev := h.nextEvent(t); ev["type"] != "go_away" {
		t.Errorf("event = %v, want go_away", ev)
	}
}

// A name this build does not declare must be refused rather than left to stall
// the turn waiting on a response that never comes.
func TestRelayRefusesUnknownToolCalls(t *testing.T) {
	fp := newFakeProvider(t, func(t *testing.T, conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		_ = conn.WriteJSON(map[string]any{"toolCall": map[string]any{
			"functionCalls": []map[string]any{{"id": "call-1", "name": "read_file"}},
		}})
		time.Sleep(500 * time.Millisecond)
	})
	h := newRelayHarness(t, fp)
	_ = h.nextEvent(t) // ready

	resp := fp.waitFor(t, "toolResponse")
	fns, _ := resp["functionResponses"].([]any)
	if len(fns) != 1 {
		t.Fatalf("functionResponses = %v, want one", resp["functionResponses"])
	}
	fn, _ := fns[0].(map[string]any)
	if fn["name"] != "read_file" || fn["id"] != "call-1" {
		t.Errorf("response = %v, want it matched to the call", fn)
	}
	payload, _ := fn["response"].(map[string]any)
	if payload["error"] == nil {
		t.Errorf("response payload = %v, want an error", payload)
	}
}

// Mic audio must reach the provider as base64 realtimeInput at the declared
// input rate.
func TestRelayForwardsMicAudio(t *testing.T) {
	fp := newFakeProvider(t, func(t *testing.T, conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		time.Sleep(700 * time.Millisecond)
	})
	h := newRelayHarness(t, fp)
	_ = h.nextEvent(t) // ready

	mic := []byte{0x10, 0x20, 0x30, 0x40}
	if err := h.browser.WriteMessage(websocket.BinaryMessage, mic); err != nil {
		t.Fatalf("send mic audio: %v", err)
	}

	input := fp.waitFor(t, "realtimeInput")
	audio, _ := input["audio"].(map[string]any)
	if audio["mimeType"] != voicegemini.InputMimeType {
		t.Errorf("mimeType = %v, want %q", audio["mimeType"], voicegemini.InputMimeType)
	}
	if audio["data"] != base64.StdEncoding.EncodeToString(mic) {
		t.Errorf("data = %v, want the base64 of the frame", audio["data"])
	}
}

// A text frame from the browser is not session config; ignoring it is what
// keeps the page from choosing the model or the instructions.
func TestRelayIgnoresBrowserTextFrames(t *testing.T) {
	fp := newFakeProvider(t, func(t *testing.T, conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		time.Sleep(500 * time.Millisecond)
	})
	h := newRelayHarness(t, fp)
	_ = h.nextEvent(t) // ready

	if err := h.browser.WriteMessage(websocket.TextMessage, []byte(`{"setup":{"model":"evil"}}`)); err != nil {
		t.Fatalf("send text: %v", err)
	}
	mic := []byte{0x01, 0x02}
	if err := h.browser.WriteMessage(websocket.BinaryMessage, mic); err != nil {
		t.Fatalf("send mic: %v", err)
	}
	// The audio behind it still lands, so this is not just a dropped socket.
	fp.waitFor(t, "realtimeInput")

	for _, m := range fp.messages() {
		if setup, ok := m["setup"].(map[string]any); ok && setup["model"] == "evil" {
			t.Fatal("a browser text frame reached the provider as session config")
		}
	}
}

// The provider names the offending field when it rejects a setup payload, and
// that sentence is what the page shows.
func TestRelayForwardsProviderCloseReason(t *testing.T) {
	fp := newFakeProvider(t, nil)
	fp.closeAfterSetup = &websocket.CloseError{
		Code: websocket.CloseInvalidFramePayloadData,
		Text: `Invalid JSON payload received. Unknown name "additionalProperties"`,
	}
	h := newRelayHarness(t, fp)

	ev := h.nextEvent(t)
	if ev["type"] != "error" {
		t.Fatalf("event = %v, want error", ev)
	}
	msg, _ := ev["message"].(string)
	if !strings.Contains(msg, "additionalProperties") || !strings.Contains(msg, "1007") {
		t.Errorf("message = %q, want the provider's words and the close code", msg)
	}
}

// A vanished browser must end the relay and free the slot immediately, not at
// the session TTL — otherwise the next start hits the cap naming a session that
// exists nowhere.
func TestRelayReleasesSlotWhenBrowserDisconnects(t *testing.T) {
	fp := newFakeProvider(t, func(t *testing.T, conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		time.Sleep(2 * time.Second)
	})
	h := newRelayHarness(t, fp)
	_ = h.nextEvent(t) // ready

	if _, ok := h.server.voiceStore.get(h.session); !ok {
		t.Fatal("the session should be live while the relay runs")
	}

	h.browser.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.server.voiceStore.get(h.session); !ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the session slot survived the browser disconnect")
}

// A provider that cannot be reached must reach the page as an error, not a
// silent hang.
func TestRelayReportsDialFailure(t *testing.T) {
	s := NewServerV2(Config{Addr: "127.0.0.1:0"})
	// A port nothing listens on: the dial fails immediately.
	s.voiceGemini = voicegemini.New("AIzaSyTestKeyLongEnough",
		voicegemini.WithLiveURL("ws://127.0.0.1:1"))

	mux := http.NewServeMux()
	s.setupRoutes(mux)
	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	token := voiceToken(t, s)
	res, err := httpSrv.Client().Post(httpSrv.URL+"/api/voice/sessions?token="+token, "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer res.Body.Close()
	var created struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(res.Body).Decode(&created)

	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/api/voice/gemini/ws?token=" + token + "&session=" + created.ID
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	defer conn.Close()
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var ev map[string]any
	if err := json.Unmarshal(data, &ev); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ev["type"] != "error" {
		t.Fatalf("event = %v, want error", ev)
	}
	if msg, _ := ev["message"].(string); !strings.Contains(msg, "dial failed") {
		t.Errorf("message = %q, want a dial failure", msg)
	}
}

// EnableVoice must refuse a key or model the provider rejects, so the failure
// lands at startup rather than at the microphone.
func TestEnableVoice(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{
			name:   "accepted",
			status: http.StatusOK,
			body:   `{"supportedGenerationMethods":["bidiGenerateContent"]}`,
		},
		{
			name:    "rejected key",
			status:  http.StatusForbidden,
			body:    `nope`,
			wantErr: true,
		},
		{
			name:    "model is not Live",
			status:  http.StatusOK,
			body:    `{"supportedGenerationMethods":["generateContent"]}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			models := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer models.Close()

			s := NewServerV2(Config{Addr: "127.0.0.1:0"})
			err := s.EnableVoice(t.Context(), "AIzaSyTestKeyLongEnough",
				voicegemini.WithModelsURL(models.URL),
				voicegemini.WithHTTPClient(models.Client()))

			if tt.wantErr {
				if err == nil {
					t.Fatal("EnableVoice() = nil, want an error")
				}
				if s.voiceEnabled() {
					t.Error("voice was enabled despite the failure")
				}
				return
			}
			if err != nil {
				t.Fatalf("EnableVoice() = %v", err)
			}
			if !s.voiceEnabled() {
				t.Error("voice was not enabled")
			}
		})
	}
}

// The whole point of the feature: a tool call from the model types into the pi
// session the browser is watching, and the browser is told it happened.
//
// Only the provider is faked. The session store, the relay, the dispatch, the
// prompt sanitizer and the PTY write are all production code, so this is the
// one test that would catch the wiring being right in every part and wrong
// between them.
func TestRelayToolCallDrivesTheCodingAgent(t *testing.T) {
	// The terminal has to be registered before the call arrives; the harness
	// cannot create it until the server exists, so the script waits for it.
	bound := make(chan struct{})
	fp := newFakeProvider(t, func(t *testing.T, conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		<-bound
		_ = conn.WriteJSON(map[string]any{"toolCall": map[string]any{
			"functionCalls": []map[string]any{{
				"id":   "call-1",
				"name": voiceToolSendPrompt,
				"args": map[string]any{"prompt": "add a test for the parser"},
			}},
		}})
		time.Sleep(time.Second)
	})

	h := newRelayHarnessOn(t, fp, "term-1")
	_, pty := testBridge(t, h.server, "term-1")
	close(bound)
	_ = h.nextEvent(t) // ready

	// The provider gets its answer, so the turn can continue.
	resp := fp.waitFor(t, "toolResponse")
	fns, _ := resp["functionResponses"].([]any)
	if len(fns) != 1 {
		t.Fatalf("functionResponses = %v, want one", resp["functionResponses"])
	}
	fn, _ := fns[0].(map[string]any)
	if fn["id"] != "call-1" || fn["name"] != voiceToolSendPrompt {
		t.Errorf("response = %v, want it matched to the call", fn)
	}
	payload, _ := fn["response"].(map[string]any)
	if payload["error"] != nil {
		t.Fatalf("the tool failed: %v", payload)
	}

	// The prompt reached the terminal the browser is watching.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(pty.written.String(), "add a test") {
		time.Sleep(10 * time.Millisecond)
	}
	if got := pty.written.String(); !strings.Contains(got, "add a test for the parser") {
		t.Errorf("the pty received %q, want the dictated prompt", got)
	}

	// And the page was told, so the terminal does not appear to move on its own.
	for {
		ev := h.nextEvent(t)
		if ev["type"] == "tool_call" {
			if summary, _ := ev["summary"].(string); !strings.Contains(summary, "add a test") {
				t.Errorf("tool_call summary = %q, want the prompt", summary)
			}
			break
		}
	}
}

// The setup instruction has to carry the live session's context, or the model
// is talking about a project it knows nothing about.
func TestRelaySetupCarriesAgentContext(t *testing.T) {
	fp := newFakeProvider(t, func(t *testing.T, conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		time.Sleep(300 * time.Millisecond)
	})

	h := newRelayHarnessOn(t, fp, "term-1")
	b, _ := testBridge(t, h.server, "term-1")
	b.captureOutput([]byte("pi > ready\r\n"))
	_ = h.nextEvent(t) // ready

	setup := fp.waitFor(t, "setup")
	raw, _ := json.Marshal(setup["systemInstruction"])
	for _, want := range []string{"pi", voiceToolSendPrompt, voiceToolReadScreen} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the system instruction does not mention %q: %s", want, raw)
		}
	}
}

// A wait that ran on the read loop would stop the relay reading the provider —
// no audio out and no barge-in for the length of the wait. It must not.
func TestRelayKeepsPumpingWhileAToolBlocks(t *testing.T) {
	audio := base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4})
	bound := make(chan struct{})
	fp := newFakeProvider(t, func(t *testing.T, conn *websocket.Conn) {
		_ = conn.WriteJSON(map[string]any{"setupComplete": map[string]any{}})
		<-bound
		// A wait that will not finish early: nothing is writing to the PTY, so
		// LastOutput stays zero and WaitForIdle runs its full timeout.
		_ = conn.WriteJSON(map[string]any{"toolCall": map[string]any{
			"functionCalls": []map[string]any{{
				"id": "call-1", "name": voiceToolWait,
				"args": map[string]any{"seconds": 10},
			}},
		}})
		_ = conn.WriteJSON(map[string]any{"serverContent": map[string]any{
			"modelTurn": map[string]any{"parts": []map[string]any{
				{"inlineData": map[string]any{"mimeType": "audio/pcm;rate=24000", "data": audio}},
			}},
		}})
		time.Sleep(2 * time.Second)
	})

	h := newRelayHarnessOn(t, fp, "term-1")
	testBridge(t, h.server, "term-1")
	close(bound)
	_ = h.nextEvent(t) // ready

	// The audio queued behind the blocking tool must still arrive promptly.
	deadline := time.Now().Add(3 * time.Second)
	if err := h.browser.SetReadDeadline(deadline); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	for {
		kind, data, err := h.browser.ReadMessage()
		if err != nil {
			t.Fatalf("the relay stopped pumping while a tool ran: %v", err)
		}
		if kind == websocket.BinaryMessage {
			if len(data) != 4 {
				t.Errorf("audio frame = %v, want the 4 bytes the provider sent", data)
			}
			return
		}
	}
}
