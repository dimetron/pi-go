package tools

import (
	"google.golang.org/adk/tool"
)

// ScreenProvider returns the current terminal screen content.
// Implemented by the TUI to give the LLM visibility into what the user sees.
type ScreenProvider interface {
	ScreenContent() string
}

// ScreenInput defines the parameters for the screen tool.
type ScreenInput struct {
}

// ScreenOutput contains the captured screen content.
type ScreenOutput struct {
	// The current terminal screen content visible to the user.
	Content string `json:"content"`
}

// NewScreenTool creates a tool that captures the current terminal screen.
func NewScreenTool(provider ScreenProvider) (tool.Tool, error) {
	return newTool("screen", "Capture the current terminal screen content visible to the user. Use this to see what the user is looking at, review previous conversation, or check tool output that was displayed.", func(_ tool.Context, _ ScreenInput) (ScreenOutput, error) {
		content := provider.ScreenContent()
		if content == "" {
			content = "(screen is empty)"
		}
		return ScreenOutput{Content: content}, nil
	})
}
