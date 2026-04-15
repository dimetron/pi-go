package atif

import (
	"strings"
	"time"

	"google.golang.org/adk/session"
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

		if part.FunctionCall != nil {
			tc := ToolCall{
				ToolCallID:   part.FunctionCall.ID,
				FunctionName: part.FunctionCall.Name,
				Arguments:    part.FunctionCall.Args,
			}
			if tc.Arguments == nil {
				tc.Arguments = make(map[string]any)
			}
			step.ToolCalls = append(step.ToolCalls, tc)
		}

		if part.FunctionResponse != nil {
			if step.Observation == nil {
				step.Observation = &Observation{}
			}
			result := ObservationResult{
				SourceCallID: part.FunctionResponse.ID,
				Content:      part.FunctionResponse.Response,
			}
			// Fall back to Name if ID is empty.
			if result.SourceCallID == "" {
				result.SourceCallID = part.FunctionResponse.Name
			}
			step.Observation.Results = append(step.Observation.Results, result)
		}
	}

	step.Message = buildMessage(texts)

	// Skip steps with no meaningful content.
	if step.Message == "" && len(step.ToolCalls) == 0 && step.Observation == nil {
		return nil
	}

	return []Step{step}
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
