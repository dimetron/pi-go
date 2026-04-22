package adapter

import (
	"context"
	"fmt"

	acp "github.com/coder/acp-go-sdk"
)

// emitThought forwards reasoning content as an agent_thought_chunk update.
// Empty strings are dropped — ADK sometimes emits zero-length thought parts
// between reasoning segments and those would produce meaningless panel
// entries in Zed.
//
// Thought-ness is a property of the part (genai.Part.Thought), not of the
// event-level role. The previous role-based filter in runtime.go missed
// providers that stream reasoning interleaved with normal text; that gap
// is closed here.
func (s *Stream) emitThought(ctx context.Context, text string) error {
	if text == "" {
		return nil
	}
	if s.updater == nil {
		return nil
	}
	if err := s.updater.Update(ctx, acp.UpdateAgentThoughtText(text)); err != nil {
		return fmt.Errorf("stream: agent thought update: %w", err)
	}
	return nil
}
