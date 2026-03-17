package guardrail

import (
	"context"
	"iter"

	"google.golang.org/adk/model"
)

// WrapModel wraps an LLM model to track token usage via the guardrail tracker.
// Each response's UsageMetadata is recorded. If the daily limit is exceeded,
// subsequent calls return an error.
func WrapModel(llm model.LLM, tracker *Tracker) model.LLM {
	if tracker == nil {
		return llm
	}
	return &guardedModel{inner: llm, tracker: tracker}
}

type guardedModel struct {
	inner   model.LLM
	tracker *Tracker
}

func (g *guardedModel) Name() string {
	return g.inner.Name()
}

func (g *guardedModel) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	// Pre-check: fail fast if limit already exceeded.
	if err := g.tracker.Check(); err != nil {
		return func(yield func(*model.LLMResponse, error) bool) {
			yield(&model.LLMResponse{
				ErrorCode:    "DAILY_LIMIT_EXCEEDED",
				ErrorMessage: err.Error(),
			}, nil)
		}
	}

	inner := g.inner.GenerateContent(ctx, req, stream)

	return func(yield func(*model.LLMResponse, error) bool) {
		for resp, err := range inner {
			// Track usage from each response chunk.
			if resp != nil && resp.UsageMetadata != nil {
				u := resp.UsageMetadata
				_ = g.tracker.Add(u.PromptTokenCount, u.CandidatesTokenCount)
			}
			if !yield(resp, err) {
				return
			}
		}
	}
}
