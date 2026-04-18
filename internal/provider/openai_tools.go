package provider

import (
	"encoding/json"
	"maps"

	"github.com/openai/openai-go/v3/shared"
)

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
