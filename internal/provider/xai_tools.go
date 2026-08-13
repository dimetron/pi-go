package provider

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

// xaiToolsEnvVar enables xAI's built-in server-side tools process-wide.
// Same truthy tokens as PI_NO_GROUNDING: "1", "true", "yes", "on".
const (
	xaiToolsEnvVar        = "PI_XAI_TOOLS"
	xaiToolsDisableEnvVar = "PI_NO_XAI_TOOLS"
)

// xaiServerSideToolTypes is the built-in set from xAI's server_side_tools.py
// example: web search, X (Twitter) search, and the sandboxed code interpreter.
// Collections search and remote MCP are omitted — they need user-supplied IDs
// or a server URL, so they cannot be turned on safely by default.
var xaiServerSideToolTypes = []string{"web_search", "x_search", "code_interpreter"}

// xaiToolsDisabled reports whether PI_NO_XAI_TOOLS is set to a recognized
// truthy value.
func xaiToolsDisabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(xaiToolsDisableEnvVar)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// xaiServerSideTools returns the built-in tool objects xAI's Responses API
// executes on the server. The OpenAI SDK has first-class types for web_search
// and code_interpreter, but x_search is xAI-only, and xAI's code_interpreter
// is just `{"type":"code_interpreter"}` (no OpenAI container). Override keeps
// all three on the same raw-JSON path so the wire matches the docs exactly.
func xaiServerSideTools() []responses.ToolUnionParam {
	out := make([]responses.ToolUnionParam, 0, len(xaiServerSideToolTypes))
	for _, typ := range xaiServerSideToolTypes {
		out = append(out, xaiBuiltInTool(typ))
	}
	return out
}

func xaiBuiltInTool(typ string) responses.ToolUnionParam {
	raw, err := json.Marshal(map[string]string{"type": typ})
	if err != nil {
		// typ is a package-level constant; marshal of a string map cannot fail.
		panic("xai built-in tool: " + err.Error())
	}
	return param.Override[responses.ToolUnionParam](json.RawMessage(raw))
}

// xaiToolsEnabled reports whether the caller explicitly enabled xAI server-side tools.
func xaiToolsEnabled(configured bool) bool {
	if configured {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv(xaiToolsEnvVar)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
