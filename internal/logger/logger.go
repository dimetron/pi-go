// Package logger provides session logging for pi-go.
// Logs are written to ~/.pi-go/log/yyyy-mm-dd/session-HH-MM-SS.log
package logger

import (
	"encoding/json"
	"fmt"
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
}

// Entry represents a single log entry.
type Entry struct {
	Time    string `json:"time"`
	Type    string `json:"type"`              // "user", "llm_text", "tool_call", "tool_result", "error", "info"
	Agent   string `json:"agent,omitempty"`    // agent name (for subagents)
	Tool    string `json:"tool,omitempty"`     // tool name
	Content string `json:"content,omitempty"`  // text content or error message
	Args    any    `json:"args,omitempty"`     // tool call arguments
	Session string `json:"session,omitempty"`  // session ID (logged once at start)
	Model   string `json:"model,omitempty"`    // model name (logged once at start)
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

	return &Logger{file: f, path: logPath, enc: enc}, nil
}

// Path returns the log file path.
func (l *Logger) Path() string {
	return l.path
}

// Close closes the log file.
func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
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
	_ = l.enc.Encode(e)
}

// Info logs an informational message.
func (l *Logger) Info(msg string) {
	l.Log(Entry{Type: "info", Content: msg})
}

// Error logs an error.
func (l *Logger) Error(msg string) {
	l.Log(Entry{Type: "error", Content: msg})
}

// UserMessage logs a user prompt.
func (l *Logger) UserMessage(prompt string) {
	l.Log(Entry{Type: "user", Content: prompt})
}

// LLMText logs streamed LLM text.
func (l *Logger) LLMText(agent, text string) {
	l.Log(Entry{Type: "llm_text", Agent: agent, Content: text})
}

// ToolCall logs a tool invocation.
func (l *Logger) ToolCall(agent, tool string, args any) {
	l.Log(Entry{Type: "tool_call", Agent: agent, Tool: tool, Args: args})
}

// ToolResult logs a tool response.
func (l *Logger) ToolResult(agent, tool, content string) {
	l.Log(Entry{Type: "tool_result", Agent: agent, Tool: tool, Content: content})
}

// SessionStart logs session metadata at the beginning.
func (l *Logger) SessionStart(sessionID, model, mode string) {
	l.Log(Entry{Type: "session_start", Session: sessionID, Model: model, Content: mode})
}
