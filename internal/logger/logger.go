// Package logger provides session logging for pi-go.
// Logs are written to ~/.pi-go/log/yyyy-mm-dd/session-HH-MM-SS.log
package logger

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Logger writes structured session log entries to a file.
type Logger struct {
	mu   sync.Mutex
	file *os.File
	path string
	enc  *json.Encoder

	pendingStream      *Entry
	pendingStreamSince time.Time
	streamFlushWindow  time.Duration
}

// Entry represents a single log entry.
type Entry struct {
	Time     string `json:"time"`
	Type     string `json:"type"`               // "user", "llm_text", "thinking", "tool_call", "tool_result", "error", "info", "http_request", "http_response"
	Agent    string `json:"agent,omitempty"`    // agent name (for subagents)
	Tool     string `json:"tool,omitempty"`     // tool name
	Content  string `json:"content,omitempty"`  // text content, error message, or HTTP body
	Args     any    `json:"args,omitempty"`     // tool call arguments
	Session  string `json:"session,omitempty"`  // session ID (logged once at start)
	Model    string `json:"model,omitempty"`    // model name (logged once at start)
	Provider string `json:"provider,omitempty"` // provider name (logged once at start)
	Backend  string `json:"backend,omitempty"`  // selected backend (logged once at start)
	BaseURL  string `json:"base_url,omitempty"` // selected endpoint, without credentials

	// HTTP trace fields, set only on "http_request"/"http_response" entries
	// written under --trace-http. They are part of Entry rather than a
	// separate record type so the transport trace interleaves with the tool
	// calls and model output it caused, in one chronologically ordered file.
	Exchange  uint64              `json:"exchange,omitempty"`  // correlates a request with its response
	Method    string              `json:"method,omitempty"`    // HTTP method
	URL       string              `json:"url,omitempty"`       // full request URL
	Proto     string              `json:"proto,omitempty"`     // e.g. "HTTP/2.0"
	Status    int                 `json:"status,omitempty"`    // response status code
	Headers   map[string][]string `json:"headers,omitempty"`   // credential values already masked
	Truncated bool                `json:"truncated,omitempty"` // body was cut at the cap
	DurationM int64               `json:"duration_ms,omitempty"`
}

// New creates a new session logger.
// Log file is created at ~/.pi-go/log/yyyy-mm-dd/session-HH-MM-SS.log
func New() (*Logger, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("getting home dir: %w", err)
	}

	now := time.Now()
	dateDir := now.Format("2006-01-02")
	fileName := fmt.Sprintf("session-%s.log", now.Format("15-04-05"))
	logDir := filepath.Join(home, ".pi-go", "log", dateDir)

	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating log dir: %w", err)
	}

	logPath := filepath.Join(logDir, fileName)
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening log file: %w", err)
	}

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)

	return &Logger{
		file:              f,
		path:              logPath,
		enc:               enc,
		streamFlushWindow: 500 * time.Millisecond,
	}, nil
}

// Path returns the log file path.
func (l *Logger) Path() string {
	return l.path
}

// Close closes the log file.
func (l *Logger) Close() error {
	if l == nil {
		return nil
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.file == nil {
		return nil
	}

	l.flushPendingStreamLocked()

	err := l.file.Close()
	l.file = nil
	l.enc = nil

	return err
}

// Log writes a structured entry.
func (l *Logger) Log(e Entry) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if e.Time == "" {
		e.Time = time.Now().Format(time.RFC3339Nano)
	}

	if streamedTypes[e.Type] {
		l.coalesceLocked(e)
		return
	}

	l.flushPendingStreamLocked()
	l.writeEntryLocked(e)
}

// streamedTypes are the entry types that arrive as a long run of small deltas
// rather than as one complete message. They are merged into a single entry per
// contiguous run so a reply does not land in the log one token per line.
//
// Reasoning is streamed the same way ordinary text is, so it has to be listed
// here too: a model that thinks for six minutes emits tens of thousands of
// deltas, and writing one entry each would bury the rest of the session.
var streamedTypes = map[string]bool{
	"llm_text": true,
	"thinking": true,
}

// coalesceLocked merges a streamed delta into the pending entry, or starts a
// new pending entry when this delta belongs to a different run.
//
// A run is broken by any of: a different entry type, a different agent, or the
// flush window elapsing. Type is part of that test and not merely the agent —
// reasoning and reply text stream from the same agent, back to back, so
// matching on agent alone would concatenate a model's thinking onto its answer
// and emit the result under whichever type happened to arrive first.
func (l *Logger) coalesceLocked(e Entry) {
	sameRun := l.pendingStream != nil &&
		l.pendingStream.Type == e.Type &&
		l.pendingStream.Agent == e.Agent
	withinWindow := l.streamFlushWindow <= 0 ||
		time.Since(l.pendingStreamSince) < l.streamFlushWindow

	if sameRun && withinWindow {
		l.pendingStream.Content += e.Content
		return
	}

	l.flushPendingStreamLocked()
	pending := e
	l.pendingStream = &pending
	l.pendingStreamSince = time.Now()
}

// Info logs an informational message.
func (l *Logger) Info(msg string) {
	l.Log(Entry{Type: "info", Content: msg})
}

// Error logs an error.
func (l *Logger) Error(msg string) {
	l.Log(Entry{Type: "error", Content: msg})
}

// Errorf logs an error with formatting.
func (l *Logger) Errorf(format string, v ...any) {
	l.Log(Entry{Type: "error", Content: fmt.Sprintf(format, v...)})
}

// UserMessage logs a user prompt.
func (l *Logger) UserMessage(prompt string) {
	l.Log(Entry{Type: "user", Content: prompt})
}

// LLMText logs streamed LLM text.
func (l *Logger) LLMText(agent, text string) {
	l.Log(Entry{Type: "llm_text", Agent: agent, Content: text})
}

// Thinking logs streamed model reasoning.
//
// Reasoning is logged under its own type rather than folded into llm_text so a
// reader can still tell what the model said from what it only thought, and so
// tooling that reconstructs a transcript can drop it. It is logged at all
// because a turn that reasons and then produces nothing — a degenerate
// repetition loop, a refusal formed and discarded — otherwise leaves the
// session log with an unexplained gap between two tool results.
func (l *Logger) Thinking(agent, text string) {
	l.Log(Entry{Type: "thinking", Agent: agent, Content: text})
}

// ToolCall logs a tool invocation.
func (l *Logger) ToolCall(agent, tool string, args any) {
	l.Log(Entry{Type: "tool_call", Agent: agent, Tool: tool, Args: args})
}

// ToolResult logs a tool response.
func (l *Logger) ToolResult(agent, tool, content string) {
	l.Log(Entry{Type: "tool_result", Agent: agent, Tool: tool, Content: content})
}

// SafeBaseURL removes credentials and query/fragment data before an endpoint is
// persisted in a session log.
func SafeBaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// SessionStart logs session metadata at the beginning.
func (l *Logger) SessionStart(sessionID, model, provider, backend, baseURL, mode string) {
	l.Log(Entry{
		Type: "session_start", Session: sessionID, Model: model,
		Provider: provider, Backend: backend, BaseURL: SafeBaseURL(baseURL), Content: mode,
	})
}

func (l *Logger) flushPendingStreamLocked() {
	if l.pendingStream == nil {
		return
	}
	l.writeEntryLocked(*l.pendingStream)
	l.pendingStream = nil
	l.pendingStreamSince = time.Time{}
}

func (l *Logger) writeEntryLocked(e Entry) {
	if l.enc == nil {
		return
	}
	_ = l.enc.Encode(e)
}
