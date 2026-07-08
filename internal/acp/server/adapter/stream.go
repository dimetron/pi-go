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

	type pending struct {
		thought bool
		text    string
	}
	var batch []pending

	s.mu.Lock()
	for _, part := range ev.Content.Parts {
		if part == nil {
			continue
		}
		if part.FunctionCall != nil || part.FunctionResponse != nil {
			continue
		}
		if part.Thought {
			if part.Text != "" {
				batch = append(batch, pending{thought: true, text: part.Text})
			}
			continue
		}
		if part.Text == "" {
			continue
		}
		s.finalText.WriteString(part.Text)
		batch = append(batch, pending{text: part.Text})
	}
	updater := s.updater
	s.mu.Unlock()

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

// Final returns the accumulated assistant text with surrounding whitespace
// trimmed. Safe to call multiple times; always reflects the latest state.
func (s *Stream) Final() string {
	return strings.TrimSpace(s.finalText.String())
}
