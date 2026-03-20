package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
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

// lenientSchema generates a JSON schema for T that allows additional properties.
// This prevents LLM tool calls from failing when the model sends extra/unknown parameters.
func lenientSchema[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		return nil // fall back to auto-inference
	}
	relaxSchema(schema)
	return schema
}

// relaxSchema recursively sets AdditionalProperties to an open schema on all
// nested object schemas, so LLMs sending extra fields won't trigger validation errors.
func relaxSchema(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	// Open up additional properties on any object schema.
	if s.Type == "object" || len(s.Properties) > 0 {
		s.AdditionalProperties = &jsonschema.Schema{}
	}
	// Recurse into properties.
	for _, prop := range s.Properties {
		relaxSchema(prop)
	}
	// Recurse into array items.
	if s.Items != nil {
		relaxSchema(s.Items)
	}
	// Recurse into definitions (used by $ref).
	for _, def := range s.Definitions {
		relaxSchema(def)
	}
	for _, def := range s.Defs {
		relaxSchema(def)
	}
}

// collectCoerceProps inspects a schema and returns sets of property names
// that should be coerced from string to their expected types.
func collectCoerceProps(schema *jsonschema.Schema) (intProps, boolProps, jsonProps map[string]bool) {
	intProps = make(map[string]bool)
	boolProps = make(map[string]bool)
	jsonProps = make(map[string]bool)
	if schema == nil {
		return
	}
	for name, prop := range schema.Properties {
		switch prop.Type {
		case "integer", "number":
			intProps[name] = true
		case "boolean":
			boolProps[name] = true
		case "array", "object":
			jsonProps[name] = true
		}
	}
	return
}

// helper to create a function tool with less boilerplate.
// Uses lenient input schema that tolerates extra properties from LLMs.
// Wraps with type coercion for integer/boolean fields that LLMs may send as strings,
// and parameter alias resolution for common LLM naming mistakes.
func newTool[TArgs, TResults any](name, description string, handler functiontool.Func[TArgs, TResults], aliases ...map[string]string) (tool.Tool, error) {
	schema := lenientSchema[TArgs]()
	inner, err := functiontool.New(functiontool.Config{
		Name:        name,
		Description: description,
		InputSchema: schema,
	}, handler)
	if err != nil {
		return nil, err
	}

	intProps, boolProps, jsonProps := collectCoerceProps(schema)
	var mergedAliases map[string]string
	for _, a := range aliases {
		if mergedAliases == nil {
			mergedAliases = make(map[string]string, len(a))
		}
		for from, to := range a {
			mergedAliases[from] = to
		}
	}

	if len(intProps) > 0 || len(boolProps) > 0 || len(jsonProps) > 0 || len(mergedAliases) > 0 {
		return &coercingTool{Tool: inner, intProps: intProps, boolProps: boolProps, jsonProps: jsonProps, aliases: mergedAliases}, nil
	}
	return inner, nil
}

// coercingTool wraps a tool to coerce string parameter values to their
// expected types before ADK schema validation. This handles LLMs that
// send e.g. depth:"3" instead of depth:3.
type coercingTool struct {
	tool.Tool
	intProps  map[string]bool
	boolProps map[string]bool
	jsonProps map[string]bool   // array/object props that may arrive as JSON strings
	aliases   map[string]string // from → to parameter name remapping
}

// Declaration delegates to the inner tool.
func (c *coercingTool) Declaration() *genai.FunctionDeclaration {
	type declarer interface {
		Declaration() *genai.FunctionDeclaration
	}
	if d, ok := c.Tool.(declarer); ok {
		return d.Declaration()
	}
	return nil
}

// ProcessRequest registers the coercingTool (not the inner tool) in the request
// so that the ADK runner calls our Run method (with coercion) instead of the
// inner tool's Run directly.
func (c *coercingTool) ProcessRequest(_ tool.Context, req *model.LLMRequest) error {
	if req.Tools == nil {
		req.Tools = make(map[string]any)
	}
	name := c.Name()
	if _, ok := req.Tools[name]; ok {
		return fmt.Errorf("duplicate tool: %q", name)
	}
	req.Tools[name] = c

	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	decl := c.Declaration()
	if decl == nil {
		return nil
	}
	// Find an existing genai.Tool with FunctionDeclarations
	var funcTool *genai.Tool
	for _, t := range req.Config.Tools {
		if t != nil && t.FunctionDeclarations != nil {
			funcTool = t
			break
		}
	}
	if funcTool == nil {
		req.Config.Tools = append(req.Config.Tools, &genai.Tool{
			FunctionDeclarations: []*genai.FunctionDeclaration{decl},
		})
	} else {
		funcTool.FunctionDeclarations = append(funcTool.FunctionDeclarations, decl)
	}
	return nil
}

// Run resolves parameter aliases and coerces string values to expected types,
// then delegates to the inner tool.
func (c *coercingTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	if m, ok := args.(map[string]any); ok {
		c.aliasArgs(m)
		c.coerceArgs(m)
	}
	type runner interface {
		Run(tool.Context, any) (map[string]any, error)
	}
	if r, ok := c.Tool.(runner); ok {
		return r.Run(ctx, args)
	}
	return nil, fmt.Errorf("inner tool %s does not implement Run", c.Name())
}

// aliasArgs remaps common LLM parameter name mistakes to their canonical names.
// Only applies if the canonical name is not already present.
func (c *coercingTool) aliasArgs(m map[string]any) {
	for from, to := range c.aliases {
		if _, hasCanonical := m[to]; hasCanonical {
			continue
		}
		if v, hasAlias := m[from]; hasAlias {
			m[to] = v
			delete(m, from)
		}
	}
}

// coerceArgs converts string values to their expected types based on schema info.
func (c *coercingTool) coerceArgs(m map[string]any) {
	for k, v := range m {
		// Handle string → expected type conversion
		if s, ok := v.(string); ok {
			if c.intProps[k] {
				if i, err := strconv.ParseInt(s, 10, 64); err == nil {
					m[k] = float64(i) // JSON numbers are float64 in Go maps
				} else if f, err := strconv.ParseFloat(s, 64); err == nil {
					m[k] = f
				}
			} else if c.boolProps[k] {
				if b, err := strconv.ParseBool(s); err == nil {
					m[k] = b
				}
			} else if c.jsonProps[k] {
				// LLMs sometimes stringify JSON arrays/objects. Parse them back.
				s = strings.TrimSpace(s)
				if (strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]")) ||
					(strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) {
					var parsed any
					if err := json.Unmarshal([]byte(s), &parsed); err == nil {
						m[k] = parsed
					}
				}
			}
			continue
		}

		// Handle number → integer conversion (LLMs sometimes send integers as floats)
		if c.intProps[k] {
			switch n := v.(type) {
			case float64:
				// Already float64 from JSON, keep as-is (this is correct)
			case float32:
				m[k] = float64(n)
			case int:
				m[k] = float64(n)
			case int64:
				m[k] = float64(n)
			case int32:
				m[k] = float64(n)
			case json.Number:
				if f, err := n.Float64(); err == nil {
					m[k] = f
				}
			}
		}
	}
}
