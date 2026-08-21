// Package voicegemini implements the Gemini Live half of pi's web voice
// session: key/model verification, the BidiGenerateContent wire shapes, and the
// setup message the server-side relay opens every session with.
//
// The transport is a server-side relay, not a browser-to-provider connection.
// The Gemini Live ephemeral-token flow is SDK-documented only (its REST shape
// is not published), so pi's web server relays: the browser's only peer is this
// server's WebSocket endpoint, and the server dials the Live API with the
// long-lived GEMINI_API_KEY. The key therefore never leaves the server in any
// form — there is no ephemeral credential to mint, scope, or leak.
//
// Wire protocol verified against ai.google.dev/api/live and the Live API guide
// on 2026-08-14: WebSocket
// wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent,
// client envelopes setup | clientContent | realtimeInput | toolResponse,
// server envelopes setupComplete | serverContent | toolCall |
// toolCallCancellation | goAway; input audio is raw little-endian 16-bit PCM
// at 16kHz ("audio/pcm;rate=16000"), output audio is 24kHz PCM. Re-verify
// before trusting this code — the Live API is preview-tier and moves.
package voicegemini

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/voice"
)

// DefaultModel is the Live API native-audio model. Override with
// GEMINI_LIVE_MODEL when the account has a newer one; Verify checks whatever is
// configured against the key, and the override deliberately bypasses LiveModels
// (see there).
//
// It is itself a preview, and a dated preview being retired is the usual way
// this constant goes stale. That failure mode is survivable because
// GEMINI_LIVE_MODEL repoints the server at any Live model with no code change,
// and because Verify turns a retirement into a named boot error instead of a
// dead microphone.
const DefaultModel = "gemini-3.1-flash-live-preview"

// LiveModels are the models this build offers the *browser* for per-session
// selection. It is an allowlist against a browser naming an arbitrary model,
// not a catalog of what works — GEMINI_LIVE_MODEL bypasses it entirely,
// because an operator setting an env var on their own server is a different
// trust level from a page choosing a model, and Verify still gates whatever
// they picked. Models keeps such an override selectable so switching away from
// it is not a one-way door.
//
// Re-probe before adding to this list. Most models the key advertises do not
// serve bidiGenerateContent at all, and some that do are not assistants — a
// translate-preview model never emits a toolCall or a turnComplete, so offering
// it would fail at the microphone rather than at startup.
var LiveModels = []string{
	DefaultModel,
}

// LiveURL is the BidiGenerateContent WebSocket endpoint. The API key is passed
// as a query parameter on dial, the way the GenAI SDKs do it.
const LiveURL = "wss://generativelanguage.googleapis.com/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"

// modelsURL lists models for key verification. A 4xx here at startup is the
// fail-fast signal that every later live session would die on dial.
const modelsURL = "https://generativelanguage.googleapis.com/v1beta/models"

// InputMimeType is the realtimeInput audio format the relay forwards.
const InputMimeType = "audio/pcm;rate=16000"

// OutputSampleRate is the rate the Live API streams output audio at. The
// browser builds its playback AudioContext at this rate; it is here so the
// server and the page cannot drift apart silently.
const OutputSampleRate = 24000

// InputSampleRate is the rate the relay expects mic audio at, matching
// InputMimeType.
const InputSampleRate = 16000

// Tool mirrors the GenAI functionDeclarations shape for one exposed capability.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Creator holds the server-side Gemini Live configuration. It implements
// voice.SessionCreator, but does no provider round-trip per session: the relay
// dials the provider when the browser connects, so Create only shapes the
// transport descriptor the browser needs.
type Creator struct {
	APIKey       string
	Model        string
	Instructions string
	// InstructionsFunc, when set, is consulted when the relay opens a session
	// (SetupMessage) and wins over Instructions unless it returns empty. It is
	// the hook for an instruction that depends on live server state.
	InstructionsFunc func() string
	Tools            []Tool

	ModelsURL  string // override for tests; empty means the real endpoint
	LiveURL    string // override for tests; empty means the real endpoint
	HTTPClient *http.Client
}

// New returns a Creator for the given long-lived API key.
func New(apiKey string, opts ...Option) *Creator {
	c := &Creator{
		APIKey:     apiKey,
		Model:      DefaultModel,
		HTTPClient: http.DefaultClient,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Option configures a Creator.
type Option func(*Creator)

// WithModel pins the Live model, bypassing the LiveModels allowlist.
func WithModel(m string) Option {
	return func(c *Creator) {
		if m != "" {
			c.Model = m
		}
	}
}

// WithInstructions sets the live-session system instructions.
func WithInstructions(i string) Option { return func(c *Creator) { c.Instructions = i } }

// WithInstructionsFunc sets a per-session instructions source (see the field).
func WithInstructionsFunc(f func() string) Option { return func(c *Creator) { c.InstructionsFunc = f } }

// WithTools sets the function declarations exposed to the live session.
func WithTools(t []Tool) Option { return func(c *Creator) { c.Tools = t } }

// WithModelsURL overrides the models endpoint (tests only).
func WithModelsURL(u string) Option { return func(c *Creator) { c.ModelsURL = u } }

// WithLiveURL overrides the Live WebSocket endpoint (tests only).
func WithLiveURL(u string) Option { return func(c *Creator) { c.LiveURL = u } }

// WithHTTPClient overrides the HTTP client (tests only).
func WithHTTPClient(h *http.Client) Option { return func(c *Creator) { c.HTTPClient = h } }

// Models is what this Creator offers the browser to choose from: the verified
// allowlist, plus the configured model when an operator pinned something
// outside it. An operator override that could not be selected back after
// switching away would be a trap.
func (c *Creator) Models() []string {
	if slices.Contains(LiveModels, c.Model) {
		return LiveModels
	}
	return append([]string{c.Model}, LiveModels...)
}

// WithModelSelection returns a shallow copy of c bound to one browser-selected
// model, or c itself when the selection is empty, unknown, or already current.
//
// The copy is the point: two tabs must be able to run different models at once,
// so a per-session choice must never mutate the shared Creator. So is the
// allowlist check — SetupMessage's contract is that the browser cannot choose
// the model, and that survives here because the browser picks an entry from a
// server-defined list rather than supplying a model string. A stale tab naming
// a model this build no longer offers falls back to the default instead of
// failing the session.
func (c *Creator) WithModelSelection(sel string) *Creator {
	if sel == "" || sel == c.Model || !slices.Contains(c.Models(), sel) {
		return c
	}
	cp := *c
	cp.Model = sel
	return &cp
}

// WithSessionInstructions returns a copy of c whose system instruction is the
// given text, or c itself when the text is empty.
//
// It is always a copy, never a mutation, for the same reason
// WithModelSelection is: two tabs run two live sessions off one shared Creator,
// and a per-session instruction — which is what an instruction carrying the
// current terminal's contents necessarily is — must not become every session's
// instruction. Empty is a deliberate no-op so a caller that has nothing
// session-specific to say falls through to whatever WithInstructions or
// InstructionsFunc configured.
func (c *Creator) WithSessionInstructions(text string) *Creator {
	if text == "" {
		return c
	}
	cp := *c
	cp.Instructions = text
	// A per-session instruction is the more specific answer, so it must win
	// over the process-wide hook that SetupMessage otherwise prefers.
	cp.InstructionsFunc = nil
	return &cp
}

// LiveMethod is the generation method a Live (BidiGenerateContent) session
// needs. Most Gemini models exist and answer GET /models/<name> while having no
// Live support at all, so "the model exists" is not the check worth making.
const LiveMethod = "bidiGenerateContent"

// Verify checks the key and the model against the models endpoint: the key is
// accepted, the model exists, and the model actually serves the Live API. An
// invalid key fails here with the provider's own words instead of surfacing
// later as a dial failure only the server log would see.
//
// The Live check is the one that matters at the microphone. A model that exists
// but lacks bidiGenerateContent completes the HTTP verification and then kills
// every session mid-conversation with a WebSocket 1007 the browser has to
// explain — so verification asks the models endpoint what the model can do, not
// merely whether it is there.
func (c *Creator) Verify(ctx context.Context) error {
	if c.APIKey == "" {
		return fmt.Errorf("voicegemini: GEMINI_API_KEY is not set")
	}
	base := c.ModelsURL
	if base == "" {
		base = modelsURL
	}
	// Ask for the configured model directly: proves both the key and the model
	// in one round-trip.
	u := base + "/" + url.PathEscape(c.Model)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("voicegemini: verify request: %w", err)
	}
	req.Header.Set("x-goog-api-key", c.APIKey)
	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("voicegemini: verify: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close on a read-only request

	if resp.StatusCode == http.StatusOK {
		var m struct {
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		}
		if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&m); err != nil {
			return fmt.Errorf("voicegemini: verify: reading the model description for %q: %w", c.Model, err)
		}
		if !slices.Contains(m.SupportedGenerationMethods, LiveMethod) {
			return fmt.Errorf("voicegemini: model %q does not serve the Live API (no %s; it supports %s) — set GEMINI_LIVE_MODEL to a Live model",
				c.Model, LiveMethod, methodList(m.SupportedGenerationMethods))
		}
		return nil
	}

	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	msg := strings.TrimSpace(string(b))
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("voicegemini: GEMINI_API_KEY %s rejected (status %d): %s", MaskKey(c.APIKey), resp.StatusCode, msg)
	case http.StatusNotFound:
		return fmt.Errorf("voicegemini: model %q not found for this key (set GEMINI_LIVE_MODEL): %s", c.Model, msg)
	default:
		return fmt.Errorf("voicegemini: verify: status %d: %s", resp.StatusCode, msg)
	}
}

// methodList renders the model's methods for the failure message. An empty list
// is itself the finding — "supports nothing" would read as a bug in us.
func methodList(methods []string) string {
	if len(methods) == 0 {
		return "no generation methods at all"
	}
	return strings.Join(methods, ", ")
}

// MaskKey renders a key as head***tail plus its length — enough to tell two
// keys apart in a log (a stale shell export vs the one in the environment)
// without ever printing a usable credential. Short strings are shown as ***
// only.
func MaskKey(k string) string {
	if len(k) < 16 {
		return fmt.Sprintf("***(len %d)", len(k))
	}
	return fmt.Sprintf("%s***%s(len %d)", k[:6], k[len(k)-4:], len(k))
}

// DialURL is the provider WebSocket URL including the key. Only the server ever
// sees this string — never log it.
func (c *Creator) DialURL() string {
	base := c.LiveURL
	if base == "" {
		base = LiveURL
	}
	return base + "?key=" + url.QueryEscape(c.APIKey)
}

// SessionTTL bounds how long a created session descriptor stays usable before
// the browser must ask for a new one.
const SessionTTL = 30 * time.Minute

// Create implements voice.SessionCreator. The Realtime map tells the browser to
// use pi's relay transport; it carries no credential at all, because the relay
// authenticates by web session and dials the provider itself.
func (c *Creator) Create(_ context.Context, _ string) (voice.Session, error) {
	if c.APIKey == "" {
		return voice.Session{}, fmt.Errorf("voicegemini: GEMINI_API_KEY is not set")
	}
	return voice.Session{
		ExpiresAt: time.Now().Add(SessionTTL),
		Realtime: map[string]any{
			"transport":  "gemini",
			"ws":         "/api/voice/gemini/ws",
			"model":      c.Model,
			"inputRate":  InputSampleRate,
			"outputRate": OutputSampleRate,
		},
	}, nil
}

// ---- Wire shapes (client → provider) ----

// Setup is the BidiGenerateContentSetup message the relay sends first. The
// server owns it entirely — the browser cannot choose the model, instructions,
// or tools.
type Setup struct {
	Model             string             `json:"model"`
	GenerationConfig  *GenerationConfig  `json:"generationConfig,omitempty"`
	SystemInstruction *Content           `json:"systemInstruction,omitempty"`
	Tools             []ToolDeclarations `json:"tools,omitempty"`
	InputTranscribe   *struct{}          `json:"inputAudioTranscription,omitempty"`
	OutputTranscribe  *struct{}          `json:"outputAudioTranscription,omitempty"`
}

// GenerationConfig carries the response modalities the session asks for.
type GenerationConfig struct {
	ResponseModalities []string `json:"responseModalities,omitempty"`
}

// ToolDeclarations wraps the function declarations for one tools entry.
type ToolDeclarations struct {
	FunctionDeclarations []Tool `json:"functionDeclarations,omitempty"`
}

// Content is a minimal GenAI content: role + parts.
type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts,omitempty"`
}

// Part is one content part: text or inline binary data.
type Part struct {
	Text       string      `json:"text,omitempty"`
	InlineData *InlineData `json:"inlineData,omitempty"`
}

// InlineData is a base64 blob with its MIME type.
type InlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64
}

// SetupMessage builds the first client message of a session.
func (c *Creator) SetupMessage() map[string]any {
	setup := Setup{
		// The Live API wants the fully-qualified name.
		Model:            "models/" + c.Model,
		GenerationConfig: &GenerationConfig{ResponseModalities: []string{"AUDIO"}},
		InputTranscribe:  &struct{}{},
		OutputTranscribe: &struct{}{},
	}
	instructions := c.Instructions
	if c.InstructionsFunc != nil {
		if v := c.InstructionsFunc(); v != "" {
			instructions = v
		}
	}
	if instructions != "" {
		setup.SystemInstruction = &Content{Parts: []Part{{Text: instructions}}}
	}
	if len(c.Tools) > 0 {
		setup.Tools = []ToolDeclarations{{FunctionDeclarations: c.Tools}}
	}
	return map[string]any{"setup": setup}
}

// RealtimeAudioMessage wraps one chunk of raw PCM16@16k mic audio (already
// base64-encoded) as a realtimeInput client message.
func RealtimeAudioMessage(b64 string) map[string]any {
	return map[string]any{
		"realtimeInput": map[string]any{
			"audio": map[string]any{"mimeType": InputMimeType, "data": b64},
		},
	}
}

// ToolResponseMessage answers one or more provider function calls.
func ToolResponseMessage(responses []FunctionResponse) map[string]any {
	return map[string]any{
		"toolResponse": map[string]any{"functionResponses": responses},
	}
}

// FunctionResponse is one function result, matched to its call by ID.
type FunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

// ---- Wire shapes (provider → client) ----

// ServerMessage is one BidiGenerateContentServerMessage. Exactly one of the
// pointer fields is set.
type ServerMessage struct {
	SetupComplete *struct{}      `json:"setupComplete,omitempty"`
	ServerContent *ServerContent `json:"serverContent,omitempty"`
	ToolCall      *ToolCall      `json:"toolCall,omitempty"`
	ToolCancel    *ToolCancel    `json:"toolCallCancellation,omitempty"`
	GoAway        *GoAway        `json:"goAway,omitempty"`
}

// ServerContent is the model's side of one turn.
type ServerContent struct {
	ModelTurn      *Content       `json:"modelTurn,omitempty"`
	Interrupted    bool           `json:"interrupted,omitempty"`
	TurnComplete   bool           `json:"turnComplete,omitempty"`
	InputTranscr   *Transcription `json:"inputTranscription,omitempty"`
	OutputTranscr  *Transcription `json:"outputTranscription,omitempty"`
	GenerationDone bool           `json:"generationComplete,omitempty"`
}

// Transcription is one transcript fragment. Gemini streams true increments for
// both directions, not whole utterances.
type Transcription struct {
	Text string `json:"text,omitempty"`
}

// ToolCall carries the function calls the model wants executed.
type ToolCall struct {
	FunctionCalls []FunctionCall `json:"functionCalls,omitempty"`
}

// FunctionCall is one requested call.
type FunctionCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

// ToolCancel names calls the model no longer wants answered.
type ToolCancel struct {
	IDs []string `json:"ids,omitempty"`
}

// GoAway warns that the provider is about to end the session.
type GoAway struct {
	TimeLeft string `json:"timeLeft,omitempty"`
}

// AudioParts returns the base64 PCM payloads of a model turn, in order. The
// Live API streams output audio as inlineData parts at OutputSampleRate.
func (sc *ServerContent) AudioParts() []string {
	if sc == nil || sc.ModelTurn == nil {
		return nil
	}
	var out []string
	for _, p := range sc.ModelTurn.Parts {
		if p.InlineData != nil && strings.HasPrefix(p.InlineData.MimeType, "audio/pcm") {
			out = append(out, p.InlineData.Data)
		}
	}
	return out
}
