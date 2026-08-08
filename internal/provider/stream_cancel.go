package provider

import (
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// canceledResponse is the terminal event a streaming path yields when the turn
// is canceled mid-stream.
//
// Returning without yielding anything leaves ADK holding the last *partial*
// chunk it received. Its flow then takes the "not final" branch and aborts the
// run with `TODO: last event is not final`
// (google.golang.org/adk/v2/internal/llminternal/base_flow.go), which pi-go
// renders verbatim — so pressing Esc showed the user an unfinished TODO string
// from a dependency instead of a canceled turn.
//
// The event deliberately carries no parts: cancellation drops the half-written
// reply rather than committing it to the transcript. The Content is non-nil
// only because ADK skips responses that have neither content nor an error code,
// and a skipped response cannot become the final event.
func canceledResponse() *model.LLMResponse {
	return &model.LLMResponse{
		Partial:      false,
		TurnComplete: true,
		FinishReason: genai.FinishReasonOther,
		Content:      &genai.Content{Role: string(genai.RoleModel)},
	}
}
