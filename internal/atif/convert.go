package atif

import (
	"strings"
	"time"

	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// ConvertEvent converts a session.Event into zero or more ATIF Steps.
// The stepID parameter is the starting step ID; returned steps are numbered
// sequentially from stepID.
func ConvertEvent(event *session.Event, stepID int) []Step {
	if event == nil || event.Content == nil || len(event.Content.Parts) == 0 {
		return nil
	}

	step := Step{
		StepID:    stepID,
		Timestamp: event.Timestamp.Format(time.RFC3339Nano),
		Source:    mapSource(event),
	}

	var texts []string
	for _, part := range event.Content.Parts {
		if part.Text != "" && !part.Thought {
			texts = append(texts, part.Text)
		}
		appendToolCall(&step, part.FunctionCall)
		appendObservation(&step, part.FunctionResponse)
	}

	step.Message = buildMessage(texts)

	// Skip steps with no meaningful content.
	if step.Message == "" && len(step.ToolCalls) == 0 && step.Observation == nil {
		return nil
	}

	return []Step{step}
}

// appendToolCall records a function call on the step. Nil calls are ignored,
// and nil arguments become an empty map so the emitted JSON always carries an
// object rather than null.
func appendToolCall(step *Step, call *genai.FunctionCall) {
	if call == nil {
		return
	}
	tc := ToolCall{
		ToolCallID:   call.ID,
		FunctionName: call.Name,
		Arguments:    call.Args,
	}
	if tc.Arguments == nil {
		tc.Arguments = make(map[string]any)
	}
	step.ToolCalls = append(step.ToolCalls, tc)
}

// appendObservation records a function response on the step, creating the
// observation on first use so steps without results keep it nil. Nil responses
// are ignored.
func appendObservation(step *Step, resp *genai.FunctionResponse) {
	if resp == nil {
		return
	}
	if step.Observation == nil {
		step.Observation = &Observation{}
	}
	result := ObservationResult{
		SourceCallID: resp.ID,
		Content:      resp.Response,
	}
	// Fall back to Name if ID is empty.
	if result.SourceCallID == "" {
		result.SourceCallID = resp.Name
	}
	step.Observation.Results = append(step.Observation.Results, result)
}

// mapSource converts session event author to ATIF source.
func mapSource(event *session.Event) string {
	if event == nil {
		return "system"
	}
	switch event.Author {
	case "user":
		return "user"
	case "model", "pi", "assistant", "agent":
		return "agent"
	}

	// Prefer explicit content role when available; some runtimes persist model
	// events with non-standard authors (e.g. "pi").
	if event.Content != nil {
		switch strings.ToLower(strings.TrimSpace(event.Content.Role)) {
		case "model", "assistant":
			return "agent"
		}
	}

	// Fallback for completed model turns that carry visible text but may have a
	// custom author label. This keeps final answers visible in ATIF consumers.
	if event.Content != nil && event.TurnComplete && strings.EqualFold(strings.TrimSpace(string(event.FinishReason)), "STOP") {
		for _, part := range event.Content.Parts {
			if part != nil && part.Text != "" && !part.Thought {
				return "agent"
			}
		}
	}

	return "system"
}

// buildMessage converts text parts into the appropriate message value.
// Single text → plain string; multiple texts → []ContentPart; no text → "".
func buildMessage(texts []string) any {
	switch len(texts) {
	case 0:
		return ""
	case 1:
		return texts[0]
	default:
		parts := make([]ContentPart, len(texts))
		for i, t := range texts {
			parts[i] = ContentPart{Type: "text", Text: t}
		}
		return parts
	}
}
