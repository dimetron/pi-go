// Package adapter translates pi runtime (ADK) events into ACP session updates.
//
// The adapter is the single place where ACP protocol concerns touch the pi
// runtime, so the runtime can stay acp-agnostic. Stream owns per-turn state:
// text accumulation, tool-call tracking, and sub-agent nesting.
//
// This file ships the text and thought paths; tool-call lifecycle lives in
// toolcall.go. The runtime rewrite in Zed-08 wires these together.
package adapter

import (
	"context"
	"fmt"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	adksession "google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
)

// SessionUpdater streams session updates back to the ACP peer. A nil Updater
// silently discards updates so the adapter is usable in tests without a live
// connection.
type SessionUpdater interface {
	Update(ctx context.Context, update acp.SessionUpdate) error
}

// Stream holds per-turn state for a single ACP prompt turn. mu guards all
// state mutations (toolCalls map, nextCallSeq, subagentID, finalText).
//
// The ADK invokes BeforeToolCallbacks from concurrent goroutines (one per
// parallel tool call). Top-level tool start/end Updates are sent after mu is
// released so concurrent tool calls do not serialize on the protocol write;
// nested (sub-agent child) Updates intentionally hold mu to preserve
// content line-ordering on the parent card.
type Stream struct {
	updater     SessionUpdater
	mu          sync.Mutex
	finalText   strings.Builder
	toolCalls   map[string]*callState
	subagentID  string
	nextCallSeq int
	// dedup drops the aggregate re-send of text SSE already delivered as
	// deltas; without it every reply reaches the peer — and finalText — twice.
	dedup agent.StreamDedup
}

// New constructs a Stream that emits updates through u. A nil updater is
// accepted and results in updates being discarded — useful for tests that
// only care about Final().
func New(u SessionUpdater) *Stream {
	return &Stream{updater: u, toolCalls: map[string]*callState{}}
}

// OnEvent consumes one ADK event and forwards its parts to the peer.
//
// Rules:
//   - Nil event or nil content: no-op.
//   - User-role events are ignored; the ACP peer already knows the prompt.
//   - Thought parts (part.Thought == true) emit agent_thought_chunk and are
//     kept out of the assistant message accumulator.
//   - Function-call and function-response parts are skipped — Zed-04 emits
//     them via Before/AfterToolCallbacks.
//   - Plain text parts are streamed as agent_message_chunk and accumulated
//     into finalText.
//
// Text accumulation happens under mu; Update() calls are made after releasing
// mu so a concurrent tool callback does not block on event emission.
func (s *Stream) OnEvent(ctx context.Context, ev *adksession.Event) error {
	if ev == nil || ev.Content == nil {
		return nil
	}
	if ev.Content.Role == "user" {
		return nil
	}

	batch, updater := s.collectChunks(ev)
	return s.emitChunks(ctx, updater, batch)
}

// pendingChunk is one text fragment collected under mu and emitted once the
// lock is released: either a thought chunk or an assistant message chunk.
type pendingChunk struct {
	thought bool
	text    string
}

// collectChunks walks ev's parts under mu, accumulating assistant text into
// finalText, and returns the chunks to emit together with the updater to emit
// them through — read under the same lock so emission needs none.
func (s *Stream) collectChunks(ev *adksession.Event) ([]pendingChunk, SessionUpdater) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.dedup.BeginEvent(ev)
	var batch []pendingChunk
	for _, part := range ev.Content.Parts {
		chunk, ok := s.chunkForPart(ev, part)
		if !ok {
			continue
		}
		batch = append(batch, chunk)
	}
	return batch, s.updater
}

// chunkForPart classifies one part of ev, reporting false for everything
// OnEvent drops: nil parts, tool-call plumbing, empty text, and the aggregate
// re-send of text already streamed as deltas. Assistant text is accumulated
// into finalText here. The caller must hold mu.
func (s *Stream) chunkForPart(ev *adksession.Event, part *genai.Part) (pendingChunk, bool) {
	if part == nil {
		return pendingChunk{}, false
	}
	if part.FunctionCall != nil || part.FunctionResponse != nil {
		return pendingChunk{}, false
	}
	if part.Thought {
		if part.Text == "" {
			return pendingChunk{}, false
		}
		return pendingChunk{thought: true, text: part.Text}, true
	}
	if part.Text == "" {
		return pendingChunk{}, false
	}
	if s.dedup.SkipText(ev) {
		return pendingChunk{}, false
	}
	s.finalText.WriteString(part.Text)
	return pendingChunk{text: part.Text}, true
}

// emitChunks forwards the collected chunks to the peer in order, stopping at
// the first update error. A nil updater discards message chunks; thought
// chunks go through emitThought, which handles nil itself.
func (s *Stream) emitChunks(ctx context.Context, updater SessionUpdater, batch []pendingChunk) error {
	for _, p := range batch {
		if p.thought {
			if err := s.emitThought(ctx, p.text); err != nil {
				return err
			}
			continue
		}
		if updater == nil {
			continue
		}
		if err := updater.Update(ctx, acp.UpdateAgentMessageText(p.text)); err != nil {
			return fmt.Errorf("stream: agent message update: %w", err)
		}
	}
	return nil
}

// OnBashOutput forwards live shell activity to the ACP peer without adding it
// to the assistant's final response text.
func (s *Stream) OnBashOutput(ctx context.Context, execID, kind, content string) error {
	if content == "" {
		return nil
	}
	s.mu.Lock()
	updater := s.updater
	s.mu.Unlock()
	if updater == nil {
		return nil
	}
	if err := updater.Update(ctx, acp.UpdateAgentThoughtText(content)); err != nil {
		return fmt.Errorf("stream: bash %s update for %s: %w", kind, execID, err)
	}
	return nil
}

// Final returns the accumulated assistant text with surrounding whitespace
// trimmed. Safe to call multiple times; always reflects the latest state.
func (s *Stream) Final() string {
	return strings.TrimSpace(s.finalText.String())
}
