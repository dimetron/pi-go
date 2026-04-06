// Package atif provides ATIF (Agent Trajectory Interchange Format) v1.6
// export support. It converts pi-go session events into the standardized
// ATIF JSON format for interoperability with external tools and pipelines.
package atif

// SchemaVersion is the ATIF schema version produced by this package.
const SchemaVersion = "ATIF-v1.6"

// Trajectory is the root ATIF document.
type Trajectory struct {
	SchemaVersion          string         `json:"schema_version"`
	SessionID              string         `json:"session_id"`
	Agent                  AgentInfo      `json:"agent"`
	Steps                  []Step         `json:"steps"`
	Notes                  string         `json:"notes,omitempty"`
	FinalMetrics           *Metrics       `json:"final_metrics,omitempty"`
	ContinuedTrajectoryRef string         `json:"continued_trajectory_ref,omitempty"`
	Extra                  map[string]any `json:"extra,omitempty"`
}

// AgentInfo describes the agent that produced the trajectory.
type AgentInfo struct {
	Name            string         `json:"name"`
	Version         string         `json:"version,omitempty"`
	ModelName       string         `json:"model_name,omitempty"`
	ToolDefinitions []any          `json:"tool_definitions,omitempty"`
	Extra           map[string]any `json:"extra,omitempty"`
}

// Step represents a single interaction step in the trajectory.
type Step struct {
	StepID           int            `json:"step_id"`
	Timestamp        string         `json:"timestamp,omitempty"`
	Source           string         `json:"source"`
	Message          any            `json:"message"`
	ModelName        string         `json:"model_name,omitempty"`
	ReasoningEffort  any            `json:"reasoning_effort,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall     `json:"tool_calls,omitempty"`
	Observation      *Observation   `json:"observation,omitempty"`
	IsCopiedContext  bool           `json:"is_copied_context,omitempty"`
	Metrics          *Metrics       `json:"metrics,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
}

// ToolCall represents a tool invocation by the agent.
type ToolCall struct {
	ToolCallID   string         `json:"tool_call_id"`
	FunctionName string         `json:"function_name"`
	Arguments    map[string]any `json:"arguments"`
}

// Observation holds the results of tool executions.
type Observation struct {
	Results []ObservationResult `json:"results"`
}

// ObservationResult is a single tool execution result.
type ObservationResult struct {
	SourceCallID          string `json:"source_call_id"`
	Content               any    `json:"content"`
	SubagentTrajectoryRef string `json:"subagent_trajectory_ref,omitempty"`
}

// Metrics holds token usage and cost information.
type Metrics struct {
	PromptTokens     int            `json:"prompt_tokens,omitempty"`
	CompletionTokens int            `json:"completion_tokens,omitempty"`
	CachedTokens     int            `json:"cached_tokens,omitempty"`
	CostUSD          float64        `json:"cost_usd,omitempty"`
	Extra            map[string]any `json:"extra,omitempty"`
}

// ContentPart represents a multimodal content part (v1.6).
type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}
