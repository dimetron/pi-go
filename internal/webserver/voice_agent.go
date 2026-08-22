package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/voicegemini"
)

// Voice's tools onto the coding agent.
//
// Without these the voice session is a chat that happens to run next to pi: it
// can discuss the project but cannot touch it, and the relay used to answer
// every function call with "this voice session exposes no tools". These four
// declarations are what make voice a way to *use* pi — dictate a prompt, wait
// for the run, read what it printed, answer its permission prompts — while the
// browser terminal shows the same session updating live.
//
// The set is deliberately small. Everything a coding agent can do is already
// reachable through pi itself, so a tool per capability would only add ways for
// the voice model to be wrong; the tools it needs are the ones a person sitting
// at the keyboard would use: type, wait, look, press a key.
const (
	voiceToolSendPrompt = "pi_send_prompt"
	voiceToolReadScreen = "pi_read_screen"
	voiceToolSendKey    = "pi_send_key"
	voiceToolWait       = "pi_wait_for_agent"
)

// Screen-read bounds. The default is a whole pi frame — its banner, sidebar and
// input box come to about fifty lines, and a smaller default would hand the
// model the chrome while cutting off the answer under it. The cap keeps a
// single tool result from crowding the live conversation out of the model's
// context.
const (
	voiceScreenDefaultLines = 60
	voiceScreenMaxLines     = 200
)

// Wait bounds. The cap is well under Gemini Live's own turn patience: a tool
// that blocks longer than the provider will wait ends the turn instead of
// answering it, so a long agent run is reported as "still working" and waited
// on again rather than held in one call.
const (
	voiceWaitDefaultSeconds = 20
	voiceWaitMaxSeconds     = 45
	voiceWaitQuiet          = 1500 * time.Millisecond
)

// agentVoiceTools returns the function declarations exposed to the live voice
// session.
//
// Every schema goes through voicegemini.SanitizeSchema for the same reason the
// tool paths do: the Gemini Schema message is parsed by a strict proto-JSON
// parser that closes the session with WebSocket 1007 on any field it does not
// know, and that failure lands at the microphone rather than at startup.
func agentVoiceTools() ([]voicegemini.Tool, error) {
	raw := []struct {
		name, desc string
		schema     string
	}{
		{
			name: voiceToolSendPrompt,
			desc: "Send a prompt to the pi coding agent running in this project, exactly as if the user typed it into the terminal. " +
				"Use this for anything the user asks you to do to the code: read files, explain, edit, run tests, commit. " +
				"The agent works asynchronously — after sending, call " + voiceToolWait + " and then " + voiceToolReadScreen +
				" to find out what it did. Rewrite the user's spoken words into a clear written instruction; do not include filler.",
			schema: `{
				"type": "object",
				"properties": {
					"prompt": {
						"type": "string",
						"description": "The instruction to give the coding agent, as a written sentence."
					}
				},
				"required": ["prompt"]
			}`,
		},
		{
			name: voiceToolReadScreen,
			desc: "Read what the pi coding agent's terminal currently shows, as plain text. " +
				"Call this before telling the user what happened — never describe the agent's output from memory or guess at it. " +
				"Also useful at the start of a conversation to see what is already on screen.",
			schema: fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"lines": {
						"type": "integer",
						"description": "How many trailing lines to return (default %d, maximum %d)."
					}
				}
			}`, voiceScreenDefaultLines, voiceScreenMaxLines),
		},
		{
			name: voiceToolWait,
			desc: "Wait until the pi coding agent stops producing output, so you can report a finished result instead of a half-drawn screen. " +
				"Returns whether it went quiet; if it did not, the agent is still working and you should tell the user so and wait again.",
			schema: fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"seconds": {
						"type": "integer",
						"description": "How long to wait at most (default %d, maximum %d)."
					}
				}
			}`, voiceWaitDefaultSeconds, voiceWaitMaxSeconds),
		},
		{
			name: voiceToolSendKey,
			desc: "Press a single key in the pi terminal. Use this to answer a prompt the agent is showing (for example 'y' to approve, 'escape' to dismiss) " +
				"or 'ctrl-c' to interrupt a run the user asks you to stop. Do not use it to type text — use " + voiceToolSendPrompt + " for that.",
			schema: fmt.Sprintf(`{
				"type": "object",
				"properties": {
					"key": {
						"type": "string",
						"description": "One of: %s"
					}
				},
				"required": ["key"]
			}`, strings.Join(KeyNames(), ", ")),
		},
	}

	tools := make([]voicegemini.Tool, 0, len(raw))
	for _, t := range raw {
		schema, err := voicegemini.SanitizeSchema(json.RawMessage(t.schema))
		if err != nil {
			return nil, fmt.Errorf("voice tool %s: %w", t.name, err)
		}
		tools = append(tools, voicegemini.Tool{Name: t.name, Description: t.desc, Parameters: schema})
	}
	return tools, nil
}

// voiceInstructions is the system instruction one live session opens with. It
// is built per session rather than once at startup because it carries live
// state — which project pi is working in, which model it runs, and what is
// already on its screen — and a session that started mid-run should know what
// it walked into.
func (s *ServerV2) voiceInstructions(vs *voiceSession) string {
	project := s.cfg.Project
	if project == "" {
		project = "."
	}
	model := s.cfg.Model
	if model == "" {
		model = "its configured default"
	}

	var b strings.Builder
	fmt.Fprintf(&b, `You are the voice interface to pi, a coding agent that is running right now in the directory %s using %s.

The person speaking to you is looking at pi's terminal in a browser and is talking instead of typing — usually because their hands are busy. You are not the coding agent. You do not read or edit files yourself, and you have no shell. Everything that touches the project goes through the tools below, which drive the very pi session on their screen: shared history, shared working tree, shared approvals.

How to work:
- When they ask for something to be done to the code, call %s with a clear written version of their request, then %s, then %s, and tell them what actually happened.
- Never describe pi's output from memory or invent a plausible result. If you have not read the screen since the agent last ran, read it.
- When pi is asking for permission or a choice, say what it is asking and use %s to answer once they tell you.
- If %s reports that the agent is still working, say so briefly and wait again rather than guessing at the outcome.
- Questions about pi itself, or small talk, need no tools — just answer.

How to speak: you are being listened to, not read. Keep replies to a sentence or two. Summarize what pi did rather than reciting its output; never read out a file listing, a diff, or a stack trace verbatim — say what it means and offer the detail if they want it. Paths and identifiers get spoken plainly. Do not narrate that you are calling a tool.`,
		project, model,
		voiceToolSendPrompt, voiceToolWait, voiceToolReadScreen,
		voiceToolSendKey, voiceToolWait)

	if vs.Terminal == "" {
		b.WriteString("\n\nRIGHT NOW: this session is not attached to a pi terminal, so the tools will fail. Tell the user their browser did not report a terminal session and that they should reload the page.")
		return b.String()
	}
	bridge, ok := s.ptyPool.Get(vs.Terminal)
	if !ok {
		b.WriteString("\n\nRIGHT NOW: pi's terminal is not running. Tell the user to open the terminal in their browser before asking for work.")
		return b.String()
	}
	if screen := strings.TrimSpace(bridge.Screen(voiceScreenDefaultLines)); screen != "" {
		b.WriteString("\n\nThis is what pi's terminal showed when this conversation opened. It is a starting point, not a live view — read the screen again before reporting anything.\n\n")
		b.WriteString(screen)
	}
	return b.String()
}

// voiceToolResult is one function call's outcome, shaped for both the provider
// (as the function response) and the browser (as a transcript line).
type voiceToolResult struct {
	Response map[string]any
	Summary  string
}

// executeVoiceTool runs one function call against the bound pi session.
//
// Every failure returns a response rather than an error: a provider left
// waiting on a function response stalls the whole conversation, and a model
// told "there is no terminal attached" can say so out loud, which is the only
// outcome that helps whoever is talking.
func (s *ServerV2) executeVoiceTool(ctx context.Context, vs *voiceSession, fc voicegemini.FunctionCall) voiceToolResult {
	bridge, err := s.voiceBridge(vs)
	if err != nil {
		return voiceToolResult{
			Response: map[string]any{"error": err.Error()},
			Summary:  fc.Name + ": " + err.Error(),
		}
	}

	switch fc.Name {
	case voiceToolSendPrompt:
		return voiceSendPrompt(bridge, fc)
	case voiceToolReadScreen:
		return voiceReadScreen(bridge, fc)
	case voiceToolWait:
		return voiceWaitForAgent(ctx, bridge, fc)
	case voiceToolSendKey:
		return voiceSendKey(bridge, fc)
	default:
		// The model invented a name. Saying so is better than silence, which
		// would leave the turn hanging.
		return voiceToolResult{
			Response: map[string]any{"error": fmt.Sprintf("%q is not a tool this session exposes", fc.Name)},
			Summary:  "unknown tool: " + fc.Name,
		}
	}
}

// voiceSendPrompt types a prompt into the pi session and submits it.
func voiceSendPrompt(bridge *PtyBridge, fc voicegemini.FunctionCall) voiceToolResult {
	var args struct {
		Prompt string `json:"prompt"`
	}
	if err := decodeVoiceArgs(fc.Args, &args); err != nil {
		return voiceToolFailure(fc.Name, err)
	}
	if err := bridge.SendPrompt(args.Prompt); err != nil {
		return voiceToolFailure(fc.Name, err)
	}
	sent := sanitizePromptText(args.Prompt)
	return voiceToolResult{
		Response: map[string]any{
			"sent":   sent,
			"status": "The prompt was typed into the pi session and submitted. It is working now; wait, then read the screen.",
		},
		Summary: "→ pi: " + sent,
	}
}

// voiceReadScreen returns the tail of pi's terminal, clamped to the line budget
// a spoken answer can carry.
func voiceReadScreen(bridge *PtyBridge, fc voicegemini.FunctionCall) voiceToolResult {
	var args struct {
		Lines int `json:"lines"`
	}
	if err := decodeVoiceArgs(fc.Args, &args); err != nil {
		return voiceToolFailure(fc.Name, err)
	}
	n := args.Lines
	if n <= 0 {
		n = voiceScreenDefaultLines
	}
	if n > voiceScreenMaxLines {
		n = voiceScreenMaxLines
	}
	screen := bridge.Screen(n)
	if strings.TrimSpace(screen) == "" {
		return voiceToolResult{
			Response: map[string]any{"screen": "", "note": "pi's terminal has not drawn anything yet."},
			Summary:  "read screen (empty)",
		}
	}
	return voiceToolResult{
		Response: map[string]any{"screen": screen, "running": bridge.Alive()},
		Summary:  fmt.Sprintf("read screen (%d lines)", strings.Count(screen, "\n")+1),
	}
}

// voiceWaitForAgent blocks until pi's terminal goes quiet or the requested
// budget runs out, clamped so one call cannot hold the turn open indefinitely.
func voiceWaitForAgent(ctx context.Context, bridge *PtyBridge, fc voicegemini.FunctionCall) voiceToolResult {
	var args struct {
		Seconds int `json:"seconds"`
	}
	if err := decodeVoiceArgs(fc.Args, &args); err != nil {
		return voiceToolFailure(fc.Name, err)
	}
	secs := args.Seconds
	if secs <= 0 {
		secs = voiceWaitDefaultSeconds
	}
	if secs > voiceWaitMaxSeconds {
		secs = voiceWaitMaxSeconds
	}
	idle := bridge.WaitForIdle(ctx, voiceWaitQuiet, time.Duration(secs)*time.Second)
	if idle {
		return voiceToolResult{
			Response: map[string]any{"idle": true, "status": "The agent has stopped producing output. Read the screen to see what it did."},
			Summary:  "waited — agent idle",
		}
	}
	return voiceToolResult{
		Response: map[string]any{"idle": false, "status": fmt.Sprintf("Still working after %ds. Tell the user it is still going, then wait again.", secs)},
		Summary:  fmt.Sprintf("waited %ds — still working", secs),
	}
}

// voiceSendKey sends one key to pi's terminal.
func voiceSendKey(bridge *PtyBridge, fc voicegemini.FunctionCall) voiceToolResult {
	var args struct {
		Key string `json:"key"`
	}
	if err := decodeVoiceArgs(fc.Args, &args); err != nil {
		return voiceToolFailure(fc.Name, err)
	}
	if err := bridge.SendKey(args.Key); err != nil {
		return voiceToolFailure(fc.Name, err)
	}
	return voiceToolResult{
		Response: map[string]any{"sent": args.Key, "status": "Key sent. Read the screen to see what changed."},
		Summary:  "key: " + args.Key,
	}
}

// voiceBridge resolves the pi session a voice session drives.
func (s *ServerV2) voiceBridge(vs *voiceSession) (*PtyBridge, error) {
	if vs.Terminal == "" {
		return nil, fmt.Errorf("this voice session is not attached to a pi terminal — the page must reload to bind one")
	}
	bridge, ok := s.ptyPool.Get(vs.Terminal)
	if !ok {
		return nil, fmt.Errorf("pi's terminal is not running — open it in the browser first")
	}
	return bridge, nil
}

// voiceToolFailure shapes one tool error.
func voiceToolFailure(name string, err error) voiceToolResult {
	return voiceToolResult{
		Response: map[string]any{"error": err.Error()},
		Summary:  name + " failed: " + err.Error(),
	}
}

// decodeVoiceArgs unmarshals a function call's arguments. Absent arguments are
// not an error: every tool here has a usable default for the whole set, and a
// model that omitted an optional field should get the default rather than a
// refusal.
func decodeVoiceArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("the arguments were not the shape this tool declares: %w", err)
	}
	return nil
}
