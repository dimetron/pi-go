package agent

import (
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// Gemini rejects a request that carries a built-in tool alongside function
// declarations unless tool_config.include_server_side_tool_invocations is set:
//
//	400 INVALID_ARGUMENT — Please enable
//	tool_config.include_server_side_tool_invocations to use Built-in tools
//	with Function calling.
//
// Without this flag the only way to make grounding "work" is to strip every
// other tool — which is exactly the regression this guards: Gemini sessions
// came up with no bash/read/write/grep, the model hallucinated tool names, and
// every call returned "tool not found. Available tools: " with an empty list.
func TestGroundingToolEnablesServerSideToolInvocations(t *testing.T) {
	tool, ok := GeminiGroundingTool("gemini")
	if !ok {
		t.Fatal("grounding tool not returned for the gemini provider")
	}

	gt, isGrounding := tool.(groundingTool)
	if !isGrounding {
		t.Fatalf("GeminiGroundingTool returned %T, want groundingTool — the bare "+
			"geminitool.GoogleSearch does not set the tool_config flag", tool)
	}

	req := &model.LLMRequest{}
	if err := gt.ProcessRequest(nil, req); err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	if req.Config == nil || req.Config.ToolConfig == nil {
		t.Fatal("ProcessRequest did not populate Config.ToolConfig")
	}
	got := req.Config.ToolConfig.IncludeServerSideToolInvocations
	if got == nil || !*got {
		t.Errorf("IncludeServerSideToolInvocations = %v, want true — without it "+
			"Gemini 400s whenever pi's function tools are also present", got)
	}

	// The built-in search must still be added to the request.
	var hasSearch bool
	for _, tl := range req.Config.Tools {
		if tl != nil && tl.GoogleSearch != nil {
			hasSearch = true
		}
	}
	if !hasSearch {
		t.Error("GoogleSearch was not added to Config.Tools")
	}
}

// ProcessRequest must not clobber tools contributed by pi's own function tools —
// it appends the built-in search beside them.
func TestGroundingToolAppendsRatherThanReplaces(t *testing.T) {
	gt := groundingTool{}

	req := &model.LLMRequest{
		Config: &genai.GenerateContentConfig{
			Tools: []*genai.Tool{{
				FunctionDeclarations: []*genai.FunctionDeclaration{
					{Name: "bash"}, {Name: "read"},
				},
			}},
		},
	}

	if err := gt.ProcessRequest(nil, req); err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	var fnNames []string
	var hasSearch bool
	for _, tl := range req.Config.Tools {
		if tl == nil {
			continue
		}
		if tl.GoogleSearch != nil {
			hasSearch = true
		}
		for _, fd := range tl.FunctionDeclarations {
			fnNames = append(fnNames, fd.Name)
		}
	}

	if !hasSearch {
		t.Error("built-in search missing after ProcessRequest")
	}
	if len(fnNames) != 2 {
		t.Errorf("function declarations = %v, want bash and read preserved", fnNames)
	}
}
