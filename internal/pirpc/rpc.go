// Package pirpc implements the stdio NDJSON RPC protocol spoken by upstream
// pi's `--mode rpc`, so that the `pi-acp` ACP adapter
// (https://github.com/svkozak/pi-acp) can drive pi-go unmodified.
//
// This is deliberately a compatibility facade, not pi-go's preferred
// integration path. pi-go speaks ACP natively via `pi acp-server`, which needs
// no Node process and no translation layer. Prefer that unless you
// specifically need pi-acp's adapter-side features.
//
// # Wire format
//
// Commands arrive on stdin as newline-delimited JSON objects carrying a
// "type" discriminator and a client-generated "id":
//
//	{"type":"prompt","id":"<uuid>","message":"hello"}
//
// Every command is answered with exactly one response object echoing the id:
//
//	{"type":"response","id":"<uuid>","command":"prompt","success":true}
//
// Any other object written to stdout is treated by the adapter as an
// asynchronous event. Non-JSON stdout lines are tolerated by pi-acp as a
// human-readable "prelude", so stray output is not fatal — but this server
// does not rely on that.
//
// # Turn lifecycle
//
// A prompt is acknowledged immediately and executed on a goroutine, so that
// `abort` remains readable from stdin while the model is streaming. The turn
// emits agent_start, then message_update / tool_execution_* events, and always
// terminates with agent_settled. pi-acp resolves the ACP `session/prompt`
// request on agent_settled and on nothing else, so failing to emit it would
// hang the client forever; Run guarantees it via defer.
package pirpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/logger"
	"github.com/dimetron/pi-go/internal/provider"
)

// Config holds the stdio RPC server configuration.
type Config struct {
	Agent     *agent.Agent
	SessionID string
	In        io.Reader
	Out       io.Writer
	Log       *logger.Logger
	Model     string
}

// Server serves pi-compatible RPC commands over stdio.
type Server struct {
	agent     *agent.Agent
	sessionID string
	in        io.Reader
	out       io.Writer
	log       *logger.Logger
	model     string

	// writeMu serializes stdout writes. The turn goroutine and the command
	// read loop both emit, and interleaved NDJSON would be unparseable.
	writeMu sync.Mutex

	mu sync.Mutex
	// cancel aborts the in-flight turn; nil when no turn is running.
	cancel context.CancelFunc
	// toolSeq names tool calls the model did not assign an ID to.
	toolSeq int
}

// NewServer creates a stdio RPC server.
func NewServer(cfg Config) *Server {
	return &Server{
		agent:     cfg.Agent,
		sessionID: cfg.SessionID,
		in:        cfg.In,
		out:       cfg.Out,
		log:       cfg.Log,
		model:     cfg.Model,
	}
}

// command is one inbound request. Fields are a union over every command type;
// only those relevant to the discriminator are populated.
type command struct {
	Type string `json:"type"`
	ID   string `json:"id"`

	Message            string `json:"message"`
	Provider           string `json:"provider"`
	ModelID            string `json:"modelId"`
	Level              string `json:"level"`
	Mode               string `json:"mode"`
	Enabled            bool   `json:"enabled"`
	Name               string `json:"name"`
	CustomInstructions string `json:"customInstructions"`
	OutputPath         string `json:"outputPath"`
	SessionPath        string `json:"sessionPath"`
}

// response is the reply to a command. Exactly one is sent per command.
type response struct {
	Type    string `json:"type"`
	ID      string `json:"id,omitempty"`
	Command string `json:"command"`
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

// maxCommandBytes bounds a single command line. Prompts carry whole files, so
// the default bufio.Scanner limit of 64KiB is far too small.
const maxCommandBytes = 32 << 20

// Run reads commands until stdin closes or ctx is canceled.
//
// Input is scanned line-by-line rather than streamed through a json.Decoder:
// a decoder cannot resynchronize after a syntax error, so one malformed line
// would spin forever. Framing is newline-delimited by definition here, which
// makes each line independently recoverable.
func (s *Server) Run(ctx context.Context) error {
	sc := bufio.NewScanner(s.in)
	sc.Buffer(make([]byte, 0, 64<<10), maxCommandBytes)

	for sc.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var cmd command
		if err := json.Unmarshal(line, &cmd); err != nil {
			// Not fatal: the adapter may be a version we do not expect.
			// Report and keep serving.
			s.reply(response{Command: "", Error: "malformed command: " + err.Error()})
			continue
		}
		s.dispatch(ctx, cmd)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("reading commands: %w", err)
	}
	return nil
}

// dispatch answers one command. Everything except prompt is fast enough to
// handle inline; prompt runs on its own goroutine so abort stays responsive.
func (s *Server) dispatch(ctx context.Context, cmd command) {
	switch cmd.Type {
	case "prompt":
		if cmd.Message == "" {
			s.reply(response{ID: cmd.ID, Command: cmd.Type, Error: "message is required"})
			return
		}
		s.reply(response{ID: cmd.ID, Command: cmd.Type, Success: true})
		go s.runTurn(ctx, cmd.Message)

	case "abort":
		s.mu.Lock()
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		s.reply(response{ID: cmd.ID, Command: cmd.Type, Success: true})

	case "get_state":
		s.reply(response{ID: cmd.ID, Command: cmd.Type, Success: true, Data: s.state()})

	case "get_available_models":
		s.reply(response{ID: cmd.ID, Command: cmd.Type, Success: true, Data: availableModels()})

	case "get_session_stats":
		s.reply(response{ID: cmd.ID, Command: cmd.Type, Success: true, Data: s.state()})

	case "get_commands":
		// pi-acp merges its own built-ins into whatever we return, so an
		// empty list still yields a usable slash-command menu.
		s.reply(response{ID: cmd.ID, Command: cmd.Type, Success: true,
			Data: map[string]any{"commands": []any{}}})

	case "get_messages":
		s.reply(response{ID: cmd.ID, Command: cmd.Type, Success: true,
			Data: map[string]any{"messages": []any{}}})

	case "set_model":
		// Model selection is resolved per-run from config; record the request
		// so /model reflects it, but do not fail the command.
		if cmd.ModelID != "" {
			s.model = cmd.ModelID
		}
		s.reply(response{ID: cmd.ID, Command: cmd.Type, Success: true, Data: s.state()})

	case "set_thinking_level", "set_follow_up_mode", "set_steering_mode",
		"set_auto_compaction", "set_session_name", "switch_session", "compact":
		// Accepted so the adapter's slash commands do not error. These map to
		// pi features pi-go either handles internally or does not expose yet.
		s.reply(response{ID: cmd.ID, Command: cmd.Type, Success: true, Data: s.state()})

	case "export_html":
		s.reply(response{ID: cmd.ID, Command: cmd.Type,
			Error: "export_html is not supported by pi-go"})

	default:
		s.reply(response{ID: cmd.ID, Command: cmd.Type,
			Error: "unknown command: " + cmd.Type})
	}
}

// runTurn executes one prompt, streaming pi-shaped events. It always emits
// agent_settled, which is the only signal pi-acp accepts to resolve the turn.
func (s *Server) runTurn(ctx context.Context, message string) {
	turnCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
		s.emit(map[string]any{"type": "agent_end"})
		s.emit(map[string]any{"type": "agent_settled"})
	}()

	if s.log != nil {
		s.log.UserMessage(message)
	}
	s.emit(map[string]any{"type": "agent_start"})

	// SSE delivers the reply as deltas and then once more as an aggregate;
	// without this every text_delta is emitted twice.
	var dedup agent.StreamDedup

	for ev, err := range agent.WithRetry(agent.DefaultRetryConfig(), func() iter.Seq2[*session.Event, error] {
		return s.agent.RunStreaming(turnCtx, s.sessionID, message)
	}) {
		if err != nil {
			if turnCtx.Err() != nil {
				return
			}
			s.emitError(err)
			return
		}
		if ev == nil {
			continue
		}
		if evErr := agent.EventError(ev); evErr != nil {
			s.emitError(evErr)
			return
		}
		if ev.Content == nil {
			continue
		}

		dedup.BeginEvent(ev)
		for _, part := range ev.Content.Parts {
			switch {
			case part.Text != "" && ev.Content.Role == "thinking":
				s.emitDelta("thinking_delta", part.Text)
				if s.log != nil {
					s.log.Thinking(ev.Author, part.Text)
				}
			case part.Text != "":
				if dedup.SkipText(ev) {
					continue
				}
				s.emitDelta("text_delta", part.Text)
				if s.log != nil {
					s.log.LLMText(ev.Author, part.Text)
				}
			}

			if fc := part.FunctionCall; fc != nil {
				s.emit(map[string]any{
					"type":       "tool_execution_start",
					"toolCallId": s.toolCallID(fc.ID, fc.Name),
					"toolName":   fc.Name,
					"args":       fc.Args,
				})
				if s.log != nil {
					s.log.ToolCall(ev.Author, fc.Name, fc.Args)
				}
			}

			if fr := part.FunctionResponse; fr != nil {
				s.emit(map[string]any{
					"type":       "tool_execution_end",
					"toolCallId": s.toolCallID(fr.ID, fr.Name),
					"result":     fr.Response,
					"isError":    false,
				})
				if s.log != nil {
					if b, mErr := json.Marshal(fr.Response); mErr == nil {
						s.log.ToolResult(ev.Author, fr.Name, string(b))
					}
				}
			}
		}
	}
}

// toolCallID returns the model-assigned call id, or a synthesized stable one
// when the provider omitted it. tool_execution_start and _end must agree or
// pi-acp cannot pair them into a single tool card.
func (s *Server) toolCallID(id, name string) string {
	if id != "" {
		return id
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolSeq++
	return name + "-" + strconv.Itoa(s.toolSeq)
}

// emitDelta sends one assistant streaming chunk.
func (s *Server) emitDelta(kind, text string) {
	s.emit(map[string]any{
		"type": "message_update",
		"assistantMessageEvent": map[string]any{
			"type":  kind,
			"delta": text,
		},
	})
}

// emitError surfaces a run failure as assistant text. pi has no dedicated
// error event that pi-acp renders, so text is the only visible channel.
func (s *Server) emitError(err error) {
	if s.log != nil {
		s.log.Error(err.Error())
	}
	s.emitDelta("text_delta", "\n\nError: "+err.Error()+"\n")
}

// state reports session identity and token/cost counters. pi-acp reads
// sessionId and sessionFile from this for its session map, and the tokens
// block for /session.
func (s *Server) state() map[string]any {
	st := map[string]any{
		"sessionId":     s.sessionID,
		"model":         s.model,
		"totalMessages": 0,
		"cost":          0,
		"tokens": map[string]any{
			"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0,
		},
	}
	// Only advertise a session file that exists — pi-acp persists it into its
	// session map, and a bogus path would make session/load unresolvable.
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".pi-go", "sessions", s.sessionID+".jsonl")
		if _, statErr := os.Stat(p); statErr == nil {
			st["sessionFile"] = p
		}
	}
	return st
}

// modelEntry is one entry of the get_available_models catalog. pi-acp reads
// id and provider from each entry when resolving /model selections.
type modelEntry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

// availableModels lists pi-go's known models. pi-acp treats an empty list as
// "unauthenticated" and refuses to create a session, so this must never be
// empty; the catalog is static and needs no network call.
func availableModels() map[string]any {
	models := make([]modelEntry, 0, 64)
	for prov, names := range provider.KnownModels {
		for _, n := range names {
			models = append(models, modelEntry{ID: n, Name: n, Provider: prov})
		}
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Provider != models[j].Provider {
			return models[i].Provider < models[j].Provider
		}
		return models[i].ID < models[j].ID
	})
	return map[string]any{"models": models}
}

// reply writes a response, defaulting the envelope fields.
func (s *Server) reply(r response) {
	r.Type = "response"
	if r.Error != "" {
		r.Success = false
	}
	s.emit(r)
}

// emit writes one NDJSON object to stdout.
func (s *Server) emit(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.out.Write(append(b, '\n'))
}
