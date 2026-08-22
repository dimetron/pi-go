package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/genai"
)

// CoreOption customizes the core tool set.
type CoreOption func(*coreConfig)

type coreConfig struct {
	bashSupervisor *BashSupervisor
	readLedger     *ReadLedger
}

// WithBashSupervisor makes the bash tool use a caller-owned supervisor, so the
// caller can attach a live-output sink and stop backgrounded commands at
// shutdown. Without it CoreTools builds a private supervisor: commands still
// get process-group isolation and background handoff, but nothing outside the
// tool can see or reap them.
func WithBashSupervisor(sup *BashSupervisor) CoreOption {
	return func(c *coreConfig) { c.bashSupervisor = sup }
}

// WithReadLedger makes read, write and edit share a caller-owned ledger, so
// the read-before-write check spans a whole session rather than one tool set.
// Without it CoreTools builds a private one and the check still holds, but only
// for tools built by this call.
func WithReadLedger(l *ReadLedger) CoreOption {
	return func(c *coreConfig) { c.readLedger = l }
}

// CoreTools returns the core coding agent tools as ADK FunctionTools.
// The sandbox restricts file-system access to the given root directory.
func CoreTools(sandbox *Sandbox, opts ...CoreOption) ([]tool.Tool, error) {
	cfg := coreConfig{}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.bashSupervisor == nil {
		cfg.bashSupervisor = NewBashSupervisor()
	}
	if cfg.readLedger == nil {
		cfg.readLedger = NewReadLedger()
	}

	builders := []func(*Sandbox) (tool.Tool, error){
		func(sb *Sandbox) (tool.Tool, error) { return newReadTool(sb, cfg.readLedger) },
		newReadImageTool,
		func(sb *Sandbox) (tool.Tool, error) { return newWriteTool(sb, cfg.readLedger) },
		func(sb *Sandbox) (tool.Tool, error) { return newEditTool(sb, cfg.readLedger) },
		func(sb *Sandbox) (tool.Tool, error) { return newBashTool(sb, cfg.bashSupervisor) },
		newGrepTool,
		newFindTool,
		newLsTool,
		newTreeTool,
		newGitOverviewTool,
		newGitFileDiffTool,
		newGitHunkTool,
	}

	tools := make([]tool.Tool, 0, len(builders)+1)
	for _, b := range builders {
		t, err := b(sandbox)
		if err != nil {
			return nil, err
		}
		tools = append(tools, t)
	}

	// Add session-stats (no sandbox needed).
	sessionStatsTool, err := newSessionStatsTool()
	if err != nil {
		return nil, err
	}
	tools = append(tools, sessionStatsTool)

	return tools, nil
}

func inputSchema[T any](removeRequired bool) *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		return nil // fall back to auto-inference
	}
	relaxSchema(schema, removeRequired)
	return schema
}

// lenientSchema generates a JSON schema for T that allows additional properties
// and makes all required properties optional. This prevents LLM tool calls from
// failing before the tool can return a useful, model-visible validation error.
func lenientSchema[T any]() *jsonschema.Schema {
	return inputSchema[T](true)
}

// declarationSchema keeps required properties visible to the model while still
// allowing extra properties. The runtime schema is more forgiving; the
// declaration should remain precise so required args like find.pattern are not
// advertised as optional.
func declarationSchema[T any]() *jsonschema.Schema {
	return inputSchema[T](false)
}

// relaxSchema recursively sets AdditionalProperties to an open schema on all
// nested object schemas, so LLMs sending extra fields won't trigger validation errors.
// It optionally removes required constraints from nested schemas.
func relaxSchema(s *jsonschema.Schema, removeRequired bool) {
	if s == nil {
		return
	}
	// Open up additional properties and remove required on any object schema.
	if s.Type == "object" || len(s.Properties) > 0 {
		s.AdditionalProperties = &jsonschema.Schema{}
		if removeRequired {
			s.Required = nil
		}
	}
	// Recurse into properties.
	for _, prop := range s.Properties {
		relaxSchema(prop, removeRequired)
	}
	// Recurse into array items.
	if s.Items != nil {
		relaxSchema(s.Items, removeRequired)
	}
	// Recurse into definitions (used by $ref).
	for _, def := range s.Definitions {
		relaxSchema(def, removeRequired)
	}
	for _, def := range s.Defs {
		relaxSchema(def, removeRequired)
	}
}

// collectCoerceProps recursively inspects a schema and returns sets of property names
// that should be coerced from string to their expected types. It handles nested
// properties using dot notation (e.g., "tasks.0.agent").
func collectCoerceProps(schema *jsonschema.Schema) (intProps, boolProps, jsonProps map[string]bool) {
	intProps = make(map[string]bool)
	boolProps = make(map[string]bool)
	jsonProps = make(map[string]bool)
	if schema == nil {
		return
	}
	collectFromSchema(schema, "", intProps, boolProps, jsonProps)
	return
}

// collectFromSchema recursively traverses the schema to collect properties needing coercion.
// It registers paths using both full dot-notation paths (e.g., "tasks.$.agent") and
// simple property names (e.g., "agent") to support flexible matching during coercion.
func collectFromSchema(schema *jsonschema.Schema, prefix string, intProps, boolProps, jsonProps map[string]bool) {
	if schema == nil {
		return
	}
	for name, prop := range schema.Properties {
		fullName := name
		if prefix != "" {
			fullName = prefix + "." + name
		}
		switch effectiveSchemaType(prop) {
		case "integer", "number":
			markCoerceProp(intProps, prefix, name, fullName)
		case "boolean":
			markCoerceProp(boolProps, prefix, name, fullName)
		case "array", "object":
			markCoerceProp(jsonProps, prefix, name, fullName)
		}
		// Recurse into nested objects
		if len(prop.Properties) > 0 {
			collectFromSchema(prop, fullName, intProps, boolProps, jsonProps)
		}
		// Recurse into array items - use ".$" suffix to mark array item context
		if prop.Items != nil {
			collectFromSchema(prop.Items, fullName+".$", intProps, boolProps, jsonProps)
		}
	}
}

// effectiveSchemaType resolves the type a property coerces as. The jsonschema
// library uses Type for single types and Types for multi-types (e.g.,
// ["null", "array"] for nullable arrays), where the first non-null entry wins.
func effectiveSchemaType(prop *jsonschema.Schema) string {
	if prop.Type != "" {
		return prop.Type
	}
	for _, t := range prop.Types {
		if t != "null" {
			return t
		}
	}
	return ""
}

// markCoerceProp registers a property in one of the coercion sets under its
// full dot-notation path, and for a nested property under its bare name as
// well, so array item matching can find it.
func markCoerceProp(props map[string]bool, prefix, name, fullName string) {
	props[fullName] = true
	if prefix != "" {
		props[name] = true
	}
}

// helper to create a function tool with less boilerplate.
// Uses lenient input schema that tolerates extra properties from LLMs.
// Wraps with type coercion for integer/boolean fields that LLMs may send as strings,
// and parameter alias resolution for common LLM naming mistakes.
func newTool[TArgs, TResults any](name, description string, handler functiontool.Func[TArgs, TResults], aliases ...map[string]string) (tool.Tool, error) {
	schema := lenientSchema[TArgs]()
	declSchema := declarationSchema[TArgs]()
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

	return &coercingTool{Tool: inner, intProps: intProps, boolProps: boolProps, jsonProps: jsonProps, aliases: mergedAliases, declarationSchema: declSchema}, nil
}

// coercingTool wraps a tool to coerce string parameter values to their
// expected types before ADK schema validation. This handles LLMs that
// send e.g. depth:"3" instead of depth:3.
type coercingTool struct {
	tool.Tool
	intProps          map[string]bool
	boolProps         map[string]bool
	jsonProps         map[string]bool   // array/object props that may arrive as JSON strings
	aliases           map[string]string // from → to parameter name remapping
	declarationSchema *jsonschema.Schema
}

// Declaration delegates to the inner tool, then restores the model-facing schema
// with required properties preserved.
func (c *coercingTool) Declaration() *genai.FunctionDeclaration {
	type declarer interface {
		Declaration() *genai.FunctionDeclaration
	}
	if d, ok := c.Tool.(declarer); ok {
		decl := d.Declaration()
		if decl != nil && c.declarationSchema != nil {
			decl.ParametersJsonSchema = c.declarationSchema
		}
		return decl
	}
	return nil
}

// ProcessRequest registers the coercingTool (not the inner tool) in the request
// so that the ADK runner calls our Run method (with coercion) instead of the
// inner tool's Run directly.
func (c *coercingTool) ProcessRequest(_ agent.Context, req *model.LLMRequest) error {
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
func (c *coercingTool) Run(ctx agent.Context, args any) (map[string]any, error) {
	if m, ok := args.(map[string]any); ok {
		c.aliasArgs(m)
		c.coerceArgs(m)
	}
	type runner interface {
		Run(agent.Context, any) (map[string]any, error)
	}
	if r, ok := c.Tool.(runner); ok {
		return r.Run(ctx, args)
	}
	return nil, fmt.Errorf("inner tool %s does not implement Run", c.Name())
}

// aliasArgs remaps common LLM parameter name mistakes to their canonical names.
// If both alias and canonical are present, canonical wins but alias is still removed.
// If only alias is present, it gets renamed to the canonical name.
func (c *coercingTool) aliasArgs(m map[string]any) {
	for from, to := range c.aliases {
		if v, hasAlias := m[from]; hasAlias {
			// Only use alias if canonical is not present
			if _, hasCanonical := m[to]; !hasCanonical {
				m[to] = v
			}
			// Always remove the alias
			delete(m, from)
		}
	}
}

// coerceArgs converts string values to their expected types based on schema info.
// It handles both top-level and nested properties using dot notation (e.g., "tasks.$").
func (c *coercingTool) coerceArgs(m map[string]any) {
	for key := range m {
		c.coerceValueAtKey(m, key)
	}
}

// coerceValueAtKey processes the value at the given key, coercing if needed and recursing into nested structures.
func (c *coercingTool) coerceValueAtKey(m map[string]any, k string) {
	val := m[k]

	// Check if this key should be coerced
	if c.intProps[k] || c.boolProps[k] || c.jsonProps[k] {
		if coerced := c.tryCoerce(val, k); coerced != nil {
			m[k] = coerced
			val = coerced // Update val for potential recursion
		}
	}

	// Recurse into nested structures
	switch v := val.(type) {
	case map[string]any:
		for nestedK := range v {
			c.coerceValueAtKey(v, nestedK)
		}
	case []any:
		for i := range v {
			c.coerceArrayItem(v, i, k)
		}
	}
}

// coerceArrayItem processes an array item, building the path for nested coercion.
// When processing array items that are objects, it checks if any properties match
// the parent's ".$" suffixed paths (e.g., "tasks.$.depth" matches "depth" in array items).
func (c *coercingTool) coerceArrayItem(arr []any, idx int, parentPath string) {
	item := arr[idx]

	// Check if array itself needs coercion (e.g., string -> array)
	itemPath := parentPath
	if c.jsonProps[itemPath] {
		if coerced := c.tryCoerce(item, itemPath); coerced != nil {
			arr[idx] = coerced
			item = coerced
		}
	}

	// Recurse into the array item
	switch vv := item.(type) {
	case map[string]any:
		// Array of objects - check each property against parent.$ paths
		for nestedK := range vv {
			c.coerceArrayItemProp(vv, nestedK, parentPath)
		}
	case []any:
		// Nested array
		for i := range vv {
			c.coerceArrayItem(vv, i, parentPath)
		}
	}
}

// coerceArrayItemProp coerces one property of an object that is itself an
// array item, then recurses into whatever that property holds.
func (c *coercingTool) coerceArrayItemProp(obj map[string]any, key, parentPath string) {
	// Check both the nested key itself and parent.$ key
	nestedV := obj[key]
	nestedPath := parentPath + "." + key
	c.coerceAtPath(obj, key, nestedPath, nestedV)
	// Also check parent.$ paths (for array item properties)
	c.coerceAtPath(obj, key, parentPath+".$."+key, nestedV)

	// Recurse into nested objects/arrays
	switch nv := obj[key].(type) {
	case map[string]any:
		c.coerceValueAtKey(nv, key)
	case []any:
		for i := range nv {
			c.coerceArrayItem(nv, i, nestedPath)
		}
	}
}

// coerceAtPath stores the coercion of val at obj[key], if path is a registered
// coercion target. val is passed in rather than read back from obj so that two
// paths checked in sequence both see the value the caller started with.
func (c *coercingTool) coerceAtPath(obj map[string]any, key, path string, val any) {
	if !c.intProps[path] && !c.boolProps[path] && !c.jsonProps[path] {
		return
	}
	if coerced := c.tryCoerce(val, path); coerced != nil {
		obj[key] = coerced
	}
}

// tryCoerce attempts to coerce a value based on the property path.
// Returns the coerced value or nil if coercion was not applied.
func (c *coercingTool) tryCoerce(val any, path string) any {
	switch v := val.(type) {
	case string:
		return c.coerceStringValue(v, path)
	case float64:
		// Already float64 from JSON
		return nil
	case float32, int, int64, int32:
		// Numbers - keep as-is for intProps, convert to float64
		if c.intProps[path] {
			return numberAsFloat64(v)
		}
	case json.Number:
		if c.intProps[path] {
			if f, err := v.Float64(); err == nil {
				return f
			}
		}
	}
	return nil
}

// coerceStringValue coerces a string the model sent where the schema asked for
// a number, a boolean, or a nested JSON document.
func (c *coercingTool) coerceStringValue(v, path string) any {
	switch {
	case c.intProps[path]:
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			return float64(i) // JSON numbers are float64 in Go maps
		} else if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	case c.boolProps[path]:
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	case c.jsonProps[path]:
		// LLMs sometimes stringify JSON arrays/objects. Parse them back.
		return parseStringifiedJSON(v)
	}
	return nil
}

// parseStringifiedJSON parses a JSON array or object that arrived as a string,
// returning nil if the string is not one or does not parse.
func parseStringifiedJSON(v string) any {
	v = strings.TrimSpace(v)
	if (strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]")) ||
		(strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}")) {
		var parsed any
		if err := json.Unmarshal([]byte(v), &parsed); err == nil {
			return parsed
		}
	}
	return nil
}

// numberAsFloat64 normalizes a Go numeric value to the float64 that JSON
// decoding would have produced. It returns nil for anything else.
func numberAsFloat64(v any) any {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	}
	return nil
}
