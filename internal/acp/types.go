package acp

import (
	"strings"
	"time"
)

const (
	StatusSuccess = "success"
	StatusError   = "error"

	EventTypeMessage  = "message"
	EventTypeProgress = "progress"
	EventTypeTool     = "tool"
	EventTypeError    = "error"
	// EventTypeStderr carries a single line of subprocess stderr. Distinct
	// from Progress so the UI can render diagnostic chatter (npm install
	// progress, "API key required", protocol error dumps) differently from
	// real tool calls or agent thinking.
	EventTypeStderr = "stderr"
)

// RunRequest describes a local ACP turn request shared by client and server code.
type RunRequest struct {
	Command    []string
	Prompt     string
	SessionID  string
	CWD        string
	Env        []string
	RPCTimeout time.Duration
}

// Event is the shared local streaming model for ACP updates.
type Event struct {
	Type      string `json:"type"`
	Content   string `json:"content,omitempty"`
	Error     string `json:"error,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// RunResult is the shared local result model for ACP executions.
type RunResult struct {
	Status     string `json:"status"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Stderr     string `json:"stderr,omitempty"`     // Captured subprocess stderr for diagnostics
	StopReason string `json:"stopReason,omitempty"` // ACP stopReason from PromptResponse
}

// Validate checks whether the request has the required fields for execution.
func (r RunRequest) Validate() error {
	if len(r.Command) == 0 {
		return validationError("command is required")
	}
	for _, part := range r.Command {
		if strings.TrimSpace(part) == "" {
			return validationError("command entries must be non-empty")
		}
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return validationError("prompt is required")
	}
	return nil
}

// Validate checks whether the event carries a supported type and payload.
func (e Event) Validate() error {
	switch e.Type {
	case EventTypeMessage, EventTypeProgress, EventTypeTool, EventTypeStderr:
		if strings.TrimSpace(e.Content) == "" {
			return validationError("content is required")
		}
	case EventTypeError:
		if strings.TrimSpace(e.Error) == "" {
			return validationError("error is required")
		}
	default:
		return validationError("type is required")
	}
	return nil
}

// Validate checks whether the result carries a supported terminal status.
func (r RunResult) Validate() error {
	switch r.Status {
	case StatusSuccess:
		if strings.TrimSpace(r.Result) == "" {
			return validationError("result is required for success status")
		}
	case StatusError:
		if strings.TrimSpace(r.Error) == "" {
			return validationError("error is required for error status")
		}
	default:
		return validationError("status is required")
	}
	return nil
}

type ValidationError string

func (e ValidationError) Error() string {
	return string(e)
}

func validationError(message string) error {
	return ValidationError(message)
}
