//go:build e2e

package webserver

// Live end-to-end check of the Gemini voice relay. It self-skips without
// GEMINI_API_KEY, so `make test-e2e` stays green on a machine with no
// credentials — but a keyless run proves only plumbing. Only a run with a key
// proves the relay actually reaches the Live API, because everything this test
// covers (the dial, the setup handshake, the audio round-trip) lives on the far
// side of the provider boundary that the unit tests stop at.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dimetron/pi-go/internal/voicegemini"
)

func liveKey(t *testing.T) string {
	t.Helper()
	key := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if key == "" {
		t.Skip("GEMINI_API_KEY is not set; skipping the live Gemini voice check")
	}
	return key
}

// TestVoiceVerifyLive is the check that runs at `pi serve --voice` startup. A
// model that exists but does not serve bidiGenerateContent passes every other
// check and then kills each session mid-conversation, so this asserts against
// the real models endpoint rather than a fixture.
func TestVoiceVerifyLive(t *testing.T) {
	key := liveKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := voicegemini.New(key)
	if err := c.Verify(ctx); err != nil {
		t.Fatalf("Verify(%s) = %v", c.Model, err)
	}
}

// TestVoiceVerifyRejectsNonLiveModel proves the check has teeth: a real,
// working Gemini model that is not a Live model must fail.
func TestVoiceVerifyRejectsNonLiveModel(t *testing.T) {
	key := liveKey(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	c := voicegemini.New(key, voicegemini.WithModel("gemini-2.5-flash"))
	err := c.Verify(ctx)
	if err == nil {
		t.Fatal("Verify() = nil for a non-Live model; the startup gate would pass a model that dies at the microphone")
	}
	if !strings.Contains(err.Error(), "does not serve the Live API") {
		t.Errorf("Verify() = %v, want the Live-API rejection", err)
	}
}

// TestVoiceRelayLive drives the whole path the browser drives: enable voice,
// create a session over REST, dial the relay, and assert the provider completes
// setup. Reaching "ready" means the dial, the API key, the setup payload and
// the tool schemas were all accepted — the 1007-at-setup class of failure
// cannot reach this point.
func TestVoiceRelayLive(t *testing.T) {
	key := liveKey(t)

	s := NewServerV2(Config{Addr: "127.0.0.1:0"})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := s.EnableVoice(ctx, key); err != nil {
		t.Fatalf("EnableVoice() = %v", err)
	}

	mux := http.NewServeMux()
	s.setupRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 1. Create the session the way the page does.
	res, err := srv.Client().Post(srv.URL+"/api/voice/sessions", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("create session status = %d", res.StatusCode)
	}
	var created struct {
		ID       string         `json:"id"`
		Model    string         `json:"model"`
		Realtime map[string]any `json:"realtime"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatalf("decode session: %v", err)
	}

	// 2. Dial the relay.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") +
		created.Realtime["ws"].(string) + "?session=" + created.ID
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
			resp.Body.Close()
		}
		t.Fatalf("dial relay (status %d): %v", status, err)
	}
	defer conn.Close()
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}

	// 3. The relay dials Gemini and forwards setupComplete as "ready".
	if err := conn.SetReadDeadline(time.Now().Add(45 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for ready: %v", err)
		}
		if kind != websocket.TextMessage {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "ready":
			t.Logf("session %s ready on %s", created.ID, created.Model)
			return
		case "error":
			// The provider's own words: a rejected setup names the offending
			// field, which is the whole reason geminiCloseMessage forwards it.
			t.Fatalf("relay reported an error before ready: %s", ev.Message)
		}
	}
}

// TestVoiceRelayAudioLive sends real mic-shaped audio and asserts the model
// answers with audio. This is the assertion that cannot be faked by plumbing:
// the frames are PCM16 at the rate the relay claims, and the response comes
// back as binary frames the page can play.
func TestVoiceRelayAudioLive(t *testing.T) {
	key := liveKey(t)

	s := NewServerV2(Config{Addr: "127.0.0.1:0"})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := s.EnableVoice(ctx, key,
		voicegemini.WithInstructions("Reply with a short spoken greeting."),
	); err != nil {
		t.Fatalf("EnableVoice() = %v", err)
	}

	mux := http.NewServeMux()
	s.setupRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res, err := srv.Client().Post(srv.URL+"/api/voice/sessions", "application/json", strings.NewReader(`{}`))
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

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") +
		created.Realtime["ws"].(string) + "?session=" + created.ID
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	defer conn.Close()
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}

	// Wait for ready before speaking; audio sent before setupComplete is
	// discarded by the provider.
	for ready := false; !ready; {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for ready: %v", err)
		}
		if kind != websocket.TextMessage {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &ev)
		if ev.Type == "error" {
			t.Fatalf("relay error before ready: %s", ev.Message)
		}
		ready = ev.Type == "ready"
	}

	// Real speech, not silence. The Live API runs automatic voice-activity
	// detection: it answers when it hears an utterance end, so a stream of
	// zeroes produces nothing at all and would make this test assert that the
	// socket stayed open rather than that the session works.
	pcm := synthesizeSpeech(t, "Hello, can you hear me?")

	// 40ms frames at the input rate — the same shape the capture worklet posts,
	// paced roughly in real time because the provider's VAD is timing-sensitive.
	const frameSamples = 640
	frameBytes := frameSamples * 2
	for off := 0; off < len(pcm); off += frameBytes {
		end := min(off+frameBytes, len(pcm))
		if err := conn.WriteMessage(websocket.BinaryMessage, pcm[off:end]); err != nil {
			t.Fatalf("send audio frame at %d: %v", off, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Trailing silence so the VAD sees the utterance end.
	silence := make([]byte, frameBytes)
	for i := 0; i < 25; i++ {
		if err := conn.WriteMessage(websocket.BinaryMessage, silence); err != nil {
			t.Fatalf("send trailing silence: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The model should answer with audio, a transcript, or a turn boundary.
	// Read until the turn ends. Output audio is the assertion that matters —
	// binary frames are what the page schedules for playback — so stopping at
	// the first transcript event would leave the playback half unproven.
	var audioBytes int
	var heardUser, heardAssistant string
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if kind == websocket.BinaryMessage {
			// The page reinterprets these bytes as Int16; an odd length would
			// mean a sample was cut in half somewhere in the relay.
			if len(data)%2 != 0 {
				t.Errorf("output audio frame is %d bytes, not a whole number of PCM16 samples", len(data))
			}
			audioBytes += len(data)
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "transcript_user_delta":
			heardUser += ev.Text
		case "transcript_assistant_delta":
			heardAssistant += ev.Text
		case "error":
			t.Fatalf("relay error mid-turn: %s", ev.Text)
		}
		if ev.Type == "turn_complete" {
			break
		}
	}

	t.Logf("heard %q, answered %q, %d bytes of audio", heardUser, heardAssistant, audioBytes)

	// The transcription proves the mic path carried real speech, not that the
	// socket stayed open.
	if !strings.Contains(strings.ToLower(heardUser), "hear me") {
		t.Errorf("input transcript = %q, want it to contain the spoken phrase", heardUser)
	}
	if audioBytes == 0 {
		t.Error("the model produced no output audio; the page would have nothing to play")
	}
	if heardAssistant == "" {
		t.Error("no assistant transcript; output transcription was requested at setup")
	}
}

// synthesizeSpeech renders one sentence as the exact format the relay forwards:
// mono little-endian PCM16 at the input sample rate, header stripped.
//
// macOS `say` is the generator because it needs no fixture in the repo and no
// network. A checked-in audio file would work everywhere, but this test's whole
// value is that the audio is real speech a VAD reacts to, and a binary blob in
// git is harder to verify than a line of shell.
func synthesizeSpeech(t *testing.T, phrase string) []byte {
	t.Helper()
	if _, err := exec.LookPath("say"); err != nil {
		t.Skip("`say` is unavailable; skipping the live audio round-trip (macOS only)")
	}

	path := filepath.Join(t.TempDir(), "utterance.wav")
	format := fmt.Sprintf("LEI16@%d", voicegemini.InputSampleRate)
	cmd := exec.Command("say", "-o", path, "--data-format="+format, phrase)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("`say` failed (%v): %s", err, out)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading synthesized audio: %v", err)
	}
	pcm := stripWAVHeader(t, raw)
	if len(pcm) < voicegemini.InputSampleRate { // under half a second of audio
		t.Fatalf("synthesized audio is only %d bytes; too short to be an utterance", len(pcm))
	}
	return pcm
}

// stripWAVHeader returns the payload of the RIFF "data" chunk. The relay speaks
// headerless PCM, so shipping the 44-byte header would put "RIFF" into the
// audio stream as samples.
func stripWAVHeader(t *testing.T, raw []byte) []byte {
	t.Helper()
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		t.Fatalf("synthesized audio is not a WAV file")
	}
	// Walk the chunks rather than assuming 44 bytes: `say` writes a LIST chunk
	// before the data on some releases.
	for off := 12; off+8 <= len(raw); {
		id := string(raw[off : off+4])
		size := int(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		body := off + 8
		if id == "data" {
			return raw[body:min(body+size, len(raw))]
		}
		off = body + size + size%2 // chunks are word-aligned
	}
	t.Fatal("no data chunk in the synthesized WAV")
	return nil
}
