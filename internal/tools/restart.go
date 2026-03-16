package tools

import (
	"google.golang.org/adk/tool"
)

// RestartFunc is called when the agent invokes the restart tool.
// The TUI provides this callback to trigger a process restart.
type RestartFunc func()

// RestartInput defines the parameters for the restart tool (none needed).
type RestartInput struct {
}

// RestartOutput contains the result of the restart request.
type RestartOutput struct {
	Status string `json:"status"`
}

// NewRestartTool creates a tool that restarts the pi process.
func NewRestartTool(fn RestartFunc) (tool.Tool, error) {
	return newTool("restart", "Restart the pi process with the same arguments. Use after rebuilding the pi binary to apply changes.", func(_ tool.Context, _ RestartInput) (RestartOutput, error) {
		fn()
		return RestartOutput{Status: "restarting"}, nil
	})
}
