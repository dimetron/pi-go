package palace

import (
	"google.golang.org/adk/tool"
)

// PalaceTools returns ADK tools for interacting with the palace during agent sessions.
// Returns nil if palace is nil (palace disabled).
func PalaceTools(p *Palace) ([]tool.Tool, error) {
	if p == nil {
		return nil, nil
	}
	builders := []func(*Palace) (tool.Tool, error){
		newPalaceStatusTool,
		newPalaceSearchTool,
		newPalaceAddDrawerTool,
	}
	tools := make([]tool.Tool, 0, len(builders))
	for _, b := range builders {
		t, err := b(p)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, nil
}
