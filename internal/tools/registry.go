package tools

import (
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// CoreTools returns the core coding agent tools as ADK FunctionTools.
// The sandbox restricts file-system access to the given root directory.
func CoreTools(sandbox *Sandbox) ([]tool.Tool, error) {
	builders := []func(*Sandbox) (tool.Tool, error){
		newReadTool,
		newWriteTool,
		newEditTool,
		newBashTool,
		newGrepTool,
		newFindTool,
		newLsTool,
		newTreeTool,
		newGitOverviewTool,
		newGitFileDiffTool,
		newGitHunkTool,
	}

	tools := make([]tool.Tool, 0, len(builders))
	for _, b := range builders {
		t, err := b(sandbox)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}
	return tools, nil
}

// helper to create a function tool with less boilerplate.
func newTool[TArgs, TResults any](name, description string, handler functiontool.Func[TArgs, TResults]) (tool.Tool, error) {
	return functiontool.New(functiontool.Config{
		Name:        name,
		Description: description,
	}, handler)
}
