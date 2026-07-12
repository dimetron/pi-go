// Package codex implements a minimal client for the Codex CLI's app-server,
// which speaks JSON-RPC 2.0 over newline-delimited JSON on stdin/stdout.
//
// This is deliberately not ACP: the Codex app-server has its own protocol
// (initialize → thread/start → turn/start → notifications → turn/completed),
// so it cannot reuse the coder/acp-go-sdk plumbing in internal/acp. Only the
// methods pi-go needs for a one-shot subagent turn are implemented; the rest
// are noted as TODOs below.
package codex

import "encoding/json"

// JSON-RPC methods sent by the client.
const (
	MethodInitialize    = "initialize"
	MethodInitialized   = "initialized"
	MethodThreadStart   = "thread/start"
	MethodTurnStart     = "turn/start"
	MethodReviewStart   = "review/start"
	MethodTurnInterrupt = "turn/interrupt"
)

// TODO: future protocol methods, not needed for a one-shot subagent turn:
// thread/resume, thread/list, externalAgentConfig/import, config/read,
// account/read.

// Notification methods sent by the app-server.
const (
	NotifyThreadStarted = "thread/started"
	NotifyTurnStarted   = "turn/started"
	NotifyItemStarted   = "item/started"
	NotifyItemCompleted = "item/completed"
	NotifyTurnCompleted = "turn/completed"
	NotifyError         = "error"
)

// Sandbox modes accepted by thread/start.
const (
	SandboxReadOnly       = "read-only"
	SandboxWorkspaceWrite = "workspace-write"
)

// ApprovalNever runs the thread autonomously: the app-server never asks the
// client to approve a command or edit. Subagents are unattended, so this is
// the only policy pi-go uses.
const ApprovalNever = "never"

// Turn statuses reported on the turn/completed notification.
const (
	TurnInProgress  = "inProgress"
	TurnCompleted   = "completed"
	TurnInterrupted = "interrupted"
	TurnFailed      = "failed"
)

// Item types of the ThreadItem discriminated union.
const (
	ItemAgentMessage     = "agentMessage"
	ItemReasoning        = "reasoning"
	ItemCommandExecution = "commandExecution"
	ItemFileChange       = "fileChange"
	ItemMCPToolCall      = "mcpToolCall"
	ItemDynamicToolCall  = "dynamicToolCall"
	ItemWebSearch        = "webSearch"
	ItemExitedReviewMode = "exitedReviewMode"
)

// JSONRPCRequest is a client→server JSON-RPC 2.0 request.
type JSONRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// JSONRPCNotification is a JSON-RPC 2.0 notification: a method call with no ID
// and therefore no response.
type JSONRPCNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// JSONRPCResponse is a server→client response to a request, matched by ID.
type JSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC error object.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Error implements error so an RPC failure can be returned directly.
func (e *RPCError) Error() string { return e.Message }

// rpcMessage is the union used to decode a single line from the app-server
// before routing it. A response carries an ID and no method; a notification
// carries a method and no ID; a server-initiated request carries both.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error"`
}

// InitializeParams identifies pi-go to the app-server and opts out of the
// high-frequency delta notifications: pi-go renders whole items, so streaming
// per-token deltas would only add churn on the notification channel.
type InitializeParams struct {
	ClientInfo   ClientInfo     `json:"clientInfo"`
	Capabilities InitializeCaps `json:"capabilities"`
}

// ClientInfo describes the client to the app-server.
type ClientInfo struct {
	Title   string `json:"title"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

// InitializeCaps declares which optional protocol features the client wants.
type InitializeCaps struct {
	ExperimentalAPI           bool     `json:"experimentalApi"`
	RequestAttestation        bool     `json:"requestAttestation"`
	OptOutNotificationMethods []string `json:"optOutNotificationMethods"`
}

// InitializeResponse is the app-server's reply to initialize.
type InitializeResponse struct {
	UserAgent string `json:"userAgent"`
}

// ThreadStartParams opens a new thread rooted at CWD.
type ThreadStartParams struct {
	CWD            string  `json:"cwd"`
	Model          *string `json:"model"`
	ApprovalPolicy string  `json:"approvalPolicy"`
	Sandbox        string  `json:"sandbox"`
	ServiceName    string  `json:"serviceName"`
	Ephemeral      bool    `json:"ephemeral"`
}

// ThreadStartResponse carries the ID of the newly created thread.
type ThreadStartResponse struct {
	Thread Thread `json:"thread"`
}

// Thread is a codex conversation thread.
type Thread struct {
	ID string `json:"id"`
}

// TurnStartParams sends user input to a thread and starts a turn.
type TurnStartParams struct {
	ThreadID string      `json:"threadId"`
	Input    []UserInput `json:"input"`
	Model    *string     `json:"model"`
	Effort   *string     `json:"effort"`
	// TODO: future — OutputSchema for structured turn results.
}

// UserInput is one input block of a turn. Only text input is used.
type UserInput struct {
	Type         string `json:"type"`
	Text         string `json:"text"`
	TextElements []any  `json:"text_elements"`
}

// TurnStartResponse carries the turn created by turn/start.
type TurnStartResponse struct {
	Turn Turn `json:"turn"`
}

// Turn is a single agent turn within a thread.
type Turn struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

// ReviewStartParams starts a code review turn instead of a chat turn.
type ReviewStartParams struct {
	ThreadID string       `json:"threadId"`
	Delivery string       `json:"delivery"`
	Target   ReviewTarget `json:"target"`
}

// ReviewTarget selects what the review covers.
type ReviewTarget struct {
	Type string `json:"type"`
	// TODO: future — Branch, for the "baseBranch" target type.
}

// ReviewTargetUncommitted reviews the working tree's uncommitted changes.
const ReviewTargetUncommitted = "uncommittedChanges"

// ReviewDeliveryInline streams review findings as ordinary thread items rather
// than delivering them as a separate artifact.
const ReviewDeliveryInline = "inline"

// ReviewStartResponse carries the turn created by review/start.
type ReviewStartResponse struct {
	Turn Turn `json:"turn"`
}

// TurnInterruptParams cancels an in-flight turn.
type TurnInterruptParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
}

// TurnStartedParams is the payload of the turn/started notification.
type TurnStartedParams struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

// ItemParams is the payload of the item/started and item/completed
// notifications.
type ItemParams struct {
	ThreadID string `json:"threadId"`
	TurnID   string `json:"turnId"`
	Item     Item   `json:"item"`
}

// TurnCompletedParams is the payload of the turn/completed notification. The
// thread ID matters: codex spawns child threads for collab/subagent work and
// their completions must not be mistaken for the outer turn finishing.
type TurnCompletedParams struct {
	ThreadID string `json:"threadId"`
	Turn     Turn   `json:"turn"`
}

// ErrorParams is the payload of the error notification.
type ErrorParams struct {
	Error RPCError `json:"error"`
}

// Item is a partially-typed ThreadItem. Only the fields pi-go renders are
// decoded; the discriminator is Type.
type Item struct {
	Type     string       `json:"type"`
	ID       string       `json:"id"`
	Text     string       `json:"text,omitempty"`     // agentMessage
	Phase    string       `json:"phase,omitempty"`    // agentMessage: "final_answer" | "analysis"
	Command  string       `json:"command,omitempty"`  // commandExecution
	Status   string       `json:"status,omitempty"`   // commandExecution, fileChange, mcpToolCall
	ExitCode *int         `json:"exitCode,omitempty"` // commandExecution
	Tool     string       `json:"tool,omitempty"`     // mcpToolCall, dynamicToolCall
	Server   string       `json:"server,omitempty"`   // mcpToolCall
	Query    string       `json:"query,omitempty"`    // webSearch
	Review   string       `json:"review,omitempty"`   // exitedReviewMode
	Summary  []string     `json:"summary,omitempty"`  // reasoning
	Changes  []FileChange `json:"changes,omitempty"`  // fileChange
}

// FileChange is one path touched by a fileChange item.
type FileChange struct {
	Path string `json:"path"`
}

// PhaseFinalAnswer marks the agentMessage item that carries the turn's answer,
// as opposed to intermediate analysis chatter.
const PhaseFinalAnswer = "final_answer"
