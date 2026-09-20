package memory

import (
	"context"
	"fmt"
	"strings"

	llmmodel "google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// LLMSummarizer writes session summaries with an in-process model call.
//
// The alternative is [SubagentCompressor.SummarizeSession], which spawns the
// `memory-compressor` subagent as a child `pi` process. That path is measurably
// unusable for summarization at session end: specs/memory-fixes/research/
// findings.md F2 measures 5.6s per spawned observation against a 5s shutdown
// budget, and records the `sql: database is closed` errors that follow when the
// parent exits first. A spawned process cannot fit inside a budget the parent
// has already stopped waiting for.
//
// A single GenerateContent call is the one shape of this work that does fit:
// it is in-process, so nothing is orphaned when the agent closes, and it is one
// round trip rather than a process spawn plus a round trip.
type LLMSummarizer struct {
	llm llmmodel.LLM
}

// NewLLMSummarizer returns a summarizer backed by the given model. A nil model
// yields a summarizer that reports an error rather than panicking, so callers
// keep one code path for "summarization unavailable".
func NewLLMSummarizer(llm llmmodel.LLM) *LLMSummarizer {
	return &LLMSummarizer{llm: llm}
}

// SummarizeSession asks the model to summarize the given observations and
// parses its reply into a SessionSummary.
//
// Called with no observations it returns an error and does not call the model:
// there is nothing to summarize, and a model asked to summarize an empty list
// will invent a plausible session rather than report that it has nothing.
func (s *LLMSummarizer) SummarizeSession(ctx context.Context, sessionID, project string, observations []*Observation) (*SessionSummary, error) {
	if s == nil || s.llm == nil {
		return nil, fmt.Errorf("memory: no summarizer model configured")
	}
	if len(observations) == 0 {
		return nil, fmt.Errorf("memory: no observations to summarize for session %q", sessionID)
	}

	req := &llmmodel.LLMRequest{
		Contents: []*genai.Content{
			genai.NewContentFromText(buildSummaryPrompt(observations), genai.RoleUser),
		},
	}

	var b strings.Builder
	for resp, err := range s.llm.GenerateContent(ctx, req, false) {
		if err != nil {
			return nil, fmt.Errorf("memory: summarize session: %w", err)
		}
		if resp == nil || resp.Content == nil {
			continue
		}
		for _, part := range resp.Content.Parts {
			if part.Text != "" {
				b.WriteString(part.Text)
			}
		}
	}

	// Parsed by the same function the subagent path uses, so both summary
	// sources accept exactly the same JSON and neither can drift into
	// accepting a shape the other rejects.
	sum, err := parseSummaryResponse(b.String(), sessionID, project)
	if err != nil {
		return nil, fmt.Errorf("memory: parse session summary: %w", err)
	}
	return sum, nil
}

// SessionSummarizer summarizes one session's observations and stores the result.
//
// It is the whole end-of-session step — read the session's observations, ask
// the model once, write the summary — so the CLI and the piagent façade reach
// the store through one path and cannot disagree about what a summary is.
//
// A session with no observations is reported as an error rather than silently
// writing nothing: callers distinguish "nothing to summarize" from "summarized"
// by their own bookkeeping, but a silent success would hide a store that never
// recorded anything.
type SessionSummarizer struct {
	store Store
	llm   llmmodel.LLM
}

// NewSessionSummarizer returns a summarizer that reads observations from store
// and writes the summary back to it. A nil model, or a nil store, yields a
// summarizer that reports an error rather than panicking.
func NewSessionSummarizer(store Store, llm llmmodel.LLM) *SessionSummarizer {
	return &SessionSummarizer{store: store, llm: llm}
}

// SummarizeSession summarizes sessionID and stores the summary.
func (s *SessionSummarizer) SummarizeSession(ctx context.Context, sessionID, project string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("memory: no store configured for session summary")
	}
	if s.llm == nil {
		return fmt.Errorf("memory: no summarizer model configured")
	}

	observations, err := s.store.SessionObservations(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("memory: reading session observations: %w", err)
	}
	if len(observations) == 0 {
		return fmt.Errorf("memory: session %q has no observations to summarize", sessionID)
	}

	sum, err := NewLLMSummarizer(s.llm).SummarizeSession(ctx, sessionID, project, observations)
	if err != nil {
		return err
	}
	if err := s.store.UpsertSummary(ctx, sum); err != nil {
		return fmt.Errorf("memory: storing session summary: %w", err)
	}
	return nil
}
