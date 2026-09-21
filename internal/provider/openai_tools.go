package provider

import (
	"encoding/json"
	"maps"
	"os"
	"strings"

	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// openaiWebSearchEnvVar enables OpenAI's built-in web_search tool process-wide.
// Same truthy tokens as PI_NO_GROUNDING and PI_XAI_TOOLS: "1", "true", "yes", "on".
const (
	openaiWebSearchEnvVar        = "PI_OPENAI_WEB_SEARCH"
	openaiWebSearchDisableEnvVar = "PI_NO_OPENAI_WEB_SEARCH"
)

// openaiWebSearchDisabled reports whether PI_NO_OPENAI_WEB_SEARCH is set to a
// recognized truthy value.
func openaiWebSearchDisabled() bool {
	return truthyEnv(openaiWebSearchDisableEnvVar)
}

// openaiWebSearchEnabled reports whether web_search should be attached to the
// request: either the caller opted in, or the env var turns it on for the
// process. Opt-in rather than default-on, unlike xAI's tools — OpenAI rejects
// the tool outright on models that do not support it (gpt-4.1-nano, and gpt-5
// at minimal reasoning), so defaulting it on would break ordinary turns on
// those models. The kill switch wins over both.
func openaiWebSearchEnabled(configured bool) bool {
	if openaiWebSearchDisabled() {
		return false
	}
	return configured || truthyEnv(openaiWebSearchEnvVar)
}

// truthyEnv reports whether name holds one of the recognized truthy tokens
// (case-insensitive, trimmed). Empty, unset and any other value are false.
func truthyEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// openaiWebSearchTool returns OpenAI's built-in web_search tool. The model
// orchestrates the search server-side and cites what it read; the tool carries
// no schema of its own, which is why it is a different union variant from the
// function tools built by oaiGenaiToolsToResponses.
//
// Built through the SDK helper so the wire form tracks the pinned SDK rather
// than a hand-written map. TestOpenAIWebSearchToolWireFormat pins the bytes.
func openaiWebSearchTool() responses.ToolUnionParam {
	return responses.ToolParamOfWebSearch(responses.WebSearchToolTypeWebSearch)
}

// openaiWebSearchInclude is the include list that makes the search's sources
// readable. Without it the response carries the answer but not the URLs behind
// it, so the citations are invisible.
//
// "web_search_call.action.sources" is the complete list of URLs consulted,
// which is generally longer than the cited set and is the reason to prefer
// OpenAI's search over a snippet API. "web_search_call.results" is deliberately
// not requested: it adds the full retrieved page text, which duplicates what
// the answer already says and spends context on it.
func openaiWebSearchInclude() []responses.ResponseIncludable {
	return []responses.ResponseIncludable{
		responses.ResponseIncludableWebSearchCallActionSources,
	}
}

func oaiFunctionParameters(schema any) shared.FunctionParameters {
	paramsMap := make(shared.FunctionParameters)
	switch m := schema.(type) {
	case nil:
	case shared.FunctionParameters:
		maps.Copy(paramsMap, m)
	case map[string]any:
		maps.Copy(paramsMap, m)
	default:
		data, err := json.Marshal(schema)
		if err == nil {
			var decoded map[string]any
			if json.Unmarshal(data, &decoded) == nil {
				maps.Copy(paramsMap, decoded)
			}
		}
	}
	if _, ok := paramsMap["type"]; !ok {
		paramsMap["type"] = "object"
	}
	if paramsMap["type"] == "object" {
		if _, ok := paramsMap["properties"]; !ok {
			paramsMap["properties"] = map[string]any{}
		}
	}
	return paramsMap
}
