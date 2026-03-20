package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/subagent"
)

// maxPromptOutput is the maximum size of tool output included in the compression prompt.
const maxPromptOutput = 4096

// SubagentCompressor uses a bundled subagent to compress raw observations.
type SubagentCompressor struct {
	orchestrator *subagent.Orchestrator
}

// NewSubagentCompressor creates a compressor that uses the memory-compressor subagent.
func NewSubagentCompressor(orch *subagent.Orchestrator) *SubagentCompressor {
	return &SubagentCompressor{orchestrator: orch}
}

// compressedResponse is the JSON structure expected from the compression subagent.
type compressedResponse struct {
	Title       string   `json:"title"`
	Type        string   `json:"type"`
	Text        string   `json:"text"`
	SourceFiles []string `json:"source_files"`
}

// CompressObservation sends raw observation data to the memory-compressor subagent
// and parses the structured response.
func (c *SubagentCompressor) CompressObservation(ctx context.Context, raw RawObservation) (*Observation, error) {
	prompt := buildCompressionPrompt(raw)

	// Look up the memory-compressor agent config.
	agent, err := c.orchestrator.LookupAgent("memory-compressor")
	if err != nil {
		return nil, fmt.Errorf("finding memory-compressor agent: %w", err)
	}

	// Spawn the compression subagent.
	events, _, err := c.orchestrator.Spawn(ctx, subagent.SpawnInput{
		Agent:  agent,
		Prompt: prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("spawning memory-compressor: %w", err)
	}

	// Collect text output from the event stream.
	var result strings.Builder
	for ev := range events {
		switch ev.Type {
		case "text_delta":
			result.WriteString(ev.Content)
		case "error":
			return nil, fmt.Errorf("memory-compressor error: %s", ev.Error)
		}
	}

	// Parse the JSON response.
	obs, err := parseCompressedResponse(result.String(), raw)
	if err != nil {
		return nil, fmt.Errorf("parsing compressor response: %w", err)
	}

	return obs, nil
}

// buildCompressionPrompt creates the JSON prompt for the compression subagent.
func buildCompressionPrompt(raw RawObservation) string {
	inputJSON, _ := json.Marshal(raw.ToolInput)
	outputJSON, _ := json.Marshal(raw.ToolOutput)

	// Truncate large outputs.
	outputStr := string(outputJSON)
	if len(outputStr) > maxPromptOutput {
		outputStr = outputStr[:maxPromptOutput] + "...(truncated)"
	}

	data := map[string]string{
		"tool_name":   raw.ToolName,
		"tool_input":  string(inputJSON),
		"tool_output": outputStr,
	}
	b, _ := json.Marshal(data)
	return string(b)
}

// parseCompressedResponse extracts a structured Observation from the subagent's JSON output.
func parseCompressedResponse(text string, raw RawObservation) (*Observation, error) {
	// Strip markdown code fences if present.
	text = stripCodeFences(text)
	text = strings.TrimSpace(text)

	if text == "" {
		return nil, fmt.Errorf("empty response from compressor")
	}

	var resp compressedResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w (response: %s)", err, truncateForError(text))
	}

	if resp.Title == "" {
		return nil, fmt.Errorf("compressor returned empty title")
	}

	// Validate and default the type.
	obsType := ObservationType(resp.Type)
	if !ValidObservationTypes[obsType] {
		obsType = TypeChange
	}

	if resp.SourceFiles == nil {
		resp.SourceFiles = []string{}
	}

	return &Observation{
		SessionID:   raw.SessionID,
		Project:     raw.Project,
		Title:       resp.Title,
		Type:        obsType,
		Text:        resp.Text,
		SourceFiles: resp.SourceFiles,
		ToolName:    raw.ToolName,
		CreatedAt:   raw.Timestamp,
	}, nil
}

// stripCodeFences removes ```json ... ``` wrapping from text.
func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Remove opening fence (with optional language tag).
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		// Remove closing fence.
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
	}
	return strings.TrimSpace(s)
}

// truncateForError truncates a string for inclusion in error messages.
func truncateForError(s string) string {
	const maxLen = 200
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

// SummarizeSession creates a session summary from a list of observations.
func (c *SubagentCompressor) SummarizeSession(ctx context.Context, sessionID, project string, observations []*Observation) (*SessionSummary, error) {
	prompt := buildSummaryPrompt(observations)

	agent, err := c.orchestrator.LookupAgent("memory-compressor")
	if err != nil {
		return nil, fmt.Errorf("finding memory-compressor agent: %w", err)
	}

	events, _, err := c.orchestrator.Spawn(ctx, subagent.SpawnInput{
		Agent:  agent,
		Prompt: prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("spawning memory-compressor for summary: %w", err)
	}

	var result strings.Builder
	for ev := range events {
		switch ev.Type {
		case "text_delta":
			result.WriteString(ev.Content)
		case "error":
			return nil, fmt.Errorf("memory-compressor summary error: %s", ev.Error)
		}
	}

	return parseSummaryResponse(result.String(), sessionID, project)
}

// summaryResponse is the JSON structure expected for session summaries.
type summaryResponse struct {
	Request      string `json:"request"`
	Investigated string `json:"investigated"`
	Learned      string `json:"learned"`
	Completed    string `json:"completed"`
	NextSteps    string `json:"next_steps"`
}

// buildSummaryPrompt creates a prompt for session summarization.
func buildSummaryPrompt(observations []*Observation) string {
	var b strings.Builder
	b.WriteString("Summarize this coding session. The following observations were recorded:\n\n")
	for _, obs := range observations {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", obs.Type, obs.Title, obs.Text)
		if len(obs.SourceFiles) > 0 {
			fmt.Fprintf(&b, "  Files: %s\n", strings.Join(obs.SourceFiles, ", "))
		}
	}
	b.WriteString("\nRespond with ONLY a JSON object:\n")
	b.WriteString(`{"request": "what was the user trying to do", "investigated": "what was explored", "learned": "key discoveries", "completed": "what was accomplished", "next_steps": "suggested follow-ups"}`)
	return b.String()
}

// parseSummaryResponse parses the subagent's JSON summary output.
func parseSummaryResponse(text, sessionID, project string) (*SessionSummary, error) {
	text = stripCodeFences(text)
	text = strings.TrimSpace(text)

	if text == "" {
		return nil, fmt.Errorf("empty summary response")
	}

	var resp summaryResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return nil, fmt.Errorf("invalid summary JSON: %w", err)
	}

	return &SessionSummary{
		SessionID:    sessionID,
		Project:      project,
		Request:      resp.Request,
		Investigated: resp.Investigated,
		Learned:      resp.Learned,
		Completed:    resp.Completed,
		NextSteps:    resp.NextSteps,
		CreatedAt:    time.Now(),
	}, nil
}
