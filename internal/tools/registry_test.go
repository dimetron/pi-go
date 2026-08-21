package tools

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/model"
)

func TestCollectCoerceProps(t *testing.T) {
	t.Run("nil schema", func(t *testing.T) {
		intP, boolP, jsonP := collectCoerceProps(nil)
		if len(intP) != 0 || len(boolP) != 0 || len(jsonP) != 0 {
			t.Error("expected empty maps for nil schema")
		}
	})

	t.Run("detects integer and boolean props", func(t *testing.T) {
		schema := &jsonschema.Schema{
			Properties: map[string]*jsonschema.Schema{
				"count":   {Type: "integer"},
				"ratio":   {Type: "number"},
				"enabled": {Type: "boolean"},
				"name":    {Type: "string"},
			},
		}
		intP, boolP, _ := collectCoerceProps(schema)
		if !intP["count"] {
			t.Error("expected count in intProps")
		}
		if !intP["ratio"] {
			t.Error("expected ratio in intProps")
		}
		if !boolP["enabled"] {
			t.Error("expected enabled in boolProps")
		}
		if intP["name"] || boolP["name"] {
			t.Error("string props should not appear in int or bool maps")
		}
	})

	t.Run("empty schema", func(t *testing.T) {
		schema := &jsonschema.Schema{}
		intP, boolP, _ := collectCoerceProps(schema)
		if len(intP) != 0 || len(boolP) != 0 {
			t.Error("expected empty maps for schema with no properties")
		}
	})

	t.Run("detects nullable array via Types field", func(t *testing.T) {
		// When jsonschema generates type: ["null", "array"], prop.Type is ""
		// and prop.Types is ["null", "array"]. This must still be detected.
		schema := &jsonschema.Schema{
			Properties: map[string]*jsonschema.Schema{
				"tasks": {Types: []string{"null", "array"}, Items: &jsonschema.Schema{Type: "object"}},
				"chain": {Types: []string{"null", "array"}, Items: &jsonschema.Schema{Type: "object"}},
				"name":  {Type: "string"},
			},
		}
		_, _, jsonP := collectCoerceProps(schema)
		if !jsonP["tasks"] {
			t.Error("expected tasks in jsonProps (nullable array)")
		}
		if !jsonP["chain"] {
			t.Error("expected chain in jsonProps (nullable array)")
		}
		if jsonP["name"] {
			t.Error("string props should not appear in jsonProps")
		}
	})

	t.Run("detects nullable integer via Types field", func(t *testing.T) {
		schema := &jsonschema.Schema{
			Properties: map[string]*jsonschema.Schema{
				"timeout": {Types: []string{"null", "integer"}},
			},
		}
		intP, _, _ := collectCoerceProps(schema)
		if !intP["timeout"] {
			t.Error("expected timeout in intProps (nullable integer)")
		}
	})
}

func TestCoerceArgs(t *testing.T) {
	c := &coercingTool{
		intProps:  map[string]bool{"count": true, "depth": true},
		boolProps: map[string]bool{"verbose": true},
	}

	t.Run("coerces string int to float64", func(t *testing.T) {
		m := map[string]any{"count": "42"}
		c.coerceArgs(m)
		if v, ok := m["count"].(float64); !ok || v != 42 {
			t.Errorf("count = %v (%T), want 42.0", m["count"], m["count"])
		}
	})

	t.Run("coerces string float to float64", func(t *testing.T) {
		m := map[string]any{"depth": "3.14"}
		c.coerceArgs(m)
		if v, ok := m["depth"].(float64); !ok || v != 3.14 {
			t.Errorf("depth = %v (%T), want 3.14", m["depth"], m["depth"])
		}
	})

	t.Run("coerces string bool to bool", func(t *testing.T) {
		m := map[string]any{"verbose": "true"}
		c.coerceArgs(m)
		if v, ok := m["verbose"].(bool); !ok || !v {
			t.Errorf("verbose = %v (%T), want true", m["verbose"], m["verbose"])
		}
	})

	t.Run("leaves non-string values alone", func(t *testing.T) {
		m := map[string]any{"count": float64(10)}
		c.coerceArgs(m)
		if v, ok := m["count"].(float64); !ok || v != 10 {
			t.Errorf("count = %v (%T), want 10.0", m["count"], m["count"])
		}
	})

	t.Run("leaves unknown props alone", func(t *testing.T) {
		m := map[string]any{"unknown": "value"}
		c.coerceArgs(m)
		if m["unknown"] != "value" {
			t.Errorf("unknown = %v, want value", m["unknown"])
		}
	})

	t.Run("invalid int string not coerced", func(t *testing.T) {
		m := map[string]any{"count": "notanumber"}
		c.coerceArgs(m)
		if m["count"] != "notanumber" {
			t.Errorf("count = %v, want notanumber (should not coerce invalid)", m["count"])
		}
	})

	t.Run("invalid bool string not coerced", func(t *testing.T) {
		m := map[string]any{"verbose": "notabool"}
		c.coerceArgs(m)
		if m["verbose"] != "notabool" {
			t.Errorf("verbose = %v, want notabool", m["verbose"])
		}
	})

	t.Run("nested array items coerced", func(t *testing.T) {
		// Test that array items (objects) have their nested properties coerced
		// The schema collection produces "tasks.$.depth" for array item properties
		c := &coercingTool{
			intProps: map[string]bool{"tasks.$.depth": true}, // Path from schema collection
		}
		m := map[string]any{
			"tasks": []any{
				map[string]any{"depth": "5"},
				map[string]any{"depth": "10"},
			},
		}
		c.coerceArgs(m)

		tasks, ok := m["tasks"].([]any)
		if !ok {
			t.Fatalf("expected tasks to be []any, got %T", m["tasks"])
		}
		for i, task := range tasks {
			tm, ok := task.(map[string]any)
			if !ok {
				t.Fatalf("task %d: expected map, got %T", i, task)
			}
			d, ok := tm["depth"].(float64)
			if !ok || d != float64(i+1)*5 {
				t.Errorf("task %d: depth = %v (%T), want %v.0", i, tm["depth"], tm["depth"], float64(i+1)*5)
			}
		}
	})

	t.Run("stringified array JSON parsed back", func(t *testing.T) {
		// Test that stringified JSON arrays are parsed back
		c := &coercingTool{
			jsonProps: map[string]bool{"tasks": true},
		}
		m := map[string]any{
			"tasks": `[{"agent":"worker","task":"do stuff"}]`,
		}
		c.coerceArgs(m)

		tasks, ok := m["tasks"].([]any)
		if !ok {
			t.Fatalf("expected tasks to be []any after parsing, got %T", m["tasks"])
		}
		if len(tasks) != 1 {
			t.Fatalf("expected 1 task, got %d", len(tasks))
		}
	})
}

// treeInputWithDepth has an integer field, so newTool should produce a coercingTool.
type coercingTestInput struct {
	Depth   int  `json:"depth"`
	Verbose bool `json:"verbose"`
}
type coercingTestOutput struct {
	Result string `json:"result"`
}

type requiredDeclarationInput struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
}

type requiredDeclarationOutput struct {
	Result string `json:"result"`
}

// makeCoercingTool returns a *coercingTool by creating a tool with int/bool fields.
func makeCoercingTool(t *testing.T) *coercingTool {
	t.Helper()
	inner, err := newTool("coerce-test", "test tool with coercion",
		func(_ agent.Context, input coercingTestInput) (coercingTestOutput, error) {
			return coercingTestOutput{Result: "ok"}, nil
		},
	)
	if err != nil {
		t.Fatalf("newTool: %v", err)
	}
	ct, ok := inner.(*coercingTool)
	if !ok {
		t.Fatalf("expected *coercingTool, got %T", inner)
	}
	return ct
}

func TestNewTool_DeclarationPreservesRequiredFields(t *testing.T) {
	inner, err := newTool("required-test", "test required schema",
		func(_ agent.Context, input requiredDeclarationInput) (requiredDeclarationOutput, error) {
			return requiredDeclarationOutput{Result: input.Pattern}, nil
		},
	)
	if err != nil {
		t.Fatalf("newTool: %v", err)
	}
	ct, ok := inner.(*coercingTool)
	if !ok {
		t.Fatalf("expected *coercingTool, got %T", inner)
	}

	decl := ct.Declaration()
	if decl == nil {
		t.Fatal("Declaration() returned nil")
	}
	schema, ok := decl.ParametersJsonSchema.(*jsonschema.Schema)
	if !ok {
		t.Fatalf("ParametersJsonSchema = %T, want *jsonschema.Schema", decl.ParametersJsonSchema)
	}
	if !stringSliceContains(schema.Required, "pattern") {
		t.Fatalf("required fields = %v, want pattern", schema.Required)
	}
	if stringSliceContains(schema.Required, "path") {
		t.Fatalf("required fields = %v, did not expect optional path", schema.Required)
	}
	if schema.AdditionalProperties == nil {
		t.Fatal("expected declaration schema to allow additional properties")
	}
}

func stringSliceContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestCoercingTool_Declaration(t *testing.T) {
	ct := makeCoercingTool(t)
	decl := ct.Declaration()
	// FunctionDeclaration should be non-nil and have the right name.
	if decl == nil {
		t.Fatal("Declaration() returned nil")
	}
	if decl.Name != "coerce-test" {
		t.Errorf("Declaration().Name = %q, want %q", decl.Name, "coerce-test")
	}
}

func TestCoercingTool_ProcessRequest_RegistersTool(t *testing.T) {
	ct := makeCoercingTool(t)
	req := &model.LLMRequest{}

	err := ct.ProcessRequest(nil, req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	// The coercingTool should register itself in req.Tools
	if req.Tools == nil {
		t.Fatal("expected req.Tools to be non-nil after ProcessRequest")
	}
	if _, ok := req.Tools["coerce-test"]; !ok {
		t.Error("expected 'coerce-test' to be in req.Tools")
	}
}

func TestCoercingTool_ProcessRequest_DuplicateError(t *testing.T) {
	ct := makeCoercingTool(t)
	req := &model.LLMRequest{}

	// First registration should succeed
	if err := ct.ProcessRequest(nil, req); err != nil {
		t.Fatalf("first ProcessRequest: %v", err)
	}

	// Second registration should fail with duplicate error
	err := ct.ProcessRequest(nil, req)
	if err == nil {
		t.Error("expected duplicate tool error on second ProcessRequest")
	}
}

func TestCoercingTool_ProcessRequest_AddsDeclaration(t *testing.T) {
	ct := makeCoercingTool(t)
	req := &model.LLMRequest{}

	err := ct.ProcessRequest(nil, req)
	if err != nil {
		t.Fatalf("ProcessRequest: %v", err)
	}

	if req.Config == nil {
		t.Fatal("expected req.Config to be set")
	}
	if len(req.Config.Tools) == 0 {
		t.Fatal("expected at least one tool in req.Config.Tools")
	}
	found := false
	for _, gTool := range req.Config.Tools {
		for _, fd := range gTool.FunctionDeclarations {
			if fd.Name == "coerce-test" {
				found = true
			}
		}
	}
	if !found {
		t.Error("expected 'coerce-test' FunctionDeclaration in req.Config.Tools")
	}
}

func TestCoercingTool_ProcessRequest_AppendsToExistingFuncTool(t *testing.T) {
	// Create two different coercingTools and register both into the same request
	ct1, err := newTool("tool-one", "first",
		func(_ agent.Context, input coercingTestInput) (coercingTestOutput, error) {
			return coercingTestOutput{Result: "one"}, nil
		},
	)
	if err != nil {
		t.Fatalf("newTool tool-one: %v", err)
	}
	ct2, err := newTool("tool-two", "second",
		func(_ agent.Context, input coercingTestInput) (coercingTestOutput, error) {
			return coercingTestOutput{Result: "two"}, nil
		},
	)
	if err != nil {
		t.Fatalf("newTool tool-two: %v", err)
	}

	req := &model.LLMRequest{}
	if err := ct1.(*coercingTool).ProcessRequest(nil, req); err != nil {
		t.Fatalf("ProcessRequest tool-one: %v", err)
	}
	if err := ct2.(*coercingTool).ProcessRequest(nil, req); err != nil {
		t.Fatalf("ProcessRequest tool-two: %v", err)
	}

	// Both should share the same FunctionDeclarations list
	if len(req.Config.Tools) != 1 {
		t.Errorf("expected 1 genai.Tool entry, got %d", len(req.Config.Tools))
	}
	if len(req.Config.Tools[0].FunctionDeclarations) != 2 {
		t.Errorf("expected 2 FunctionDeclarations, got %d", len(req.Config.Tools[0].FunctionDeclarations))
	}
}

func TestCoercingTool_Run_CoercesBeforeDelegate(t *testing.T) {
	ct := makeCoercingTool(t)

	// Pass string values for int/bool fields — coercion mutates args map before delegation.
	// The inner functionTool.Run panics on a nil context, so we recover from it and just
	// verify that the coercion happened on the map before delegation was attempted.
	args := map[string]any{
		"depth":   "5",
		"verbose": "true",
	}

	func() {
		defer func() { recover() }() //nolint:errcheck
		ct.Run(nil, args)            //nolint:errcheck
	}()

	// Coercion should have mutated args before the delegate panicked
	if v, ok := args["depth"].(float64); !ok || v != 5 {
		t.Errorf("depth should have been coerced to 5.0, got %v (%T)", args["depth"], args["depth"])
	}
	if v, ok := args["verbose"].(bool); !ok || !v {
		t.Errorf("verbose should have been coerced to true, got %v (%T)", args["verbose"], args["verbose"])
	}
}

func TestCoercingTool_Run_AliasesBeforeDelegate(t *testing.T) {
	// Create a tool with aliases (path → file_path)
	inner, err := newTool("alias-test", "test tool with aliases",
		func(_ agent.Context, input coercingTestInput) (coercingTestOutput, error) {
			return coercingTestOutput{Result: "ok"}, nil
		},
		map[string]string{"path": "file_path", "glob": "pattern"},
	)
	if err != nil {
		t.Fatalf("newTool: %v", err)
	}
	ct, ok := inner.(*coercingTool)
	if !ok {
		t.Fatalf("expected *coercingTool, got %T", inner)
	}

	// "path" should be remapped to "file_path"
	args := map[string]any{"path": "/some/file.go"}
	ct.aliasArgs(args)
	if args["file_path"] != "/some/file.go" {
		t.Errorf("expected file_path=/some/file.go, got %v", args["file_path"])
	}
	if _, exists := args["path"]; exists {
		t.Error("original 'path' key should have been removed")
	}

	// If canonical name already present, alias should NOT overwrite
	args2 := map[string]any{"path": "/wrong", "file_path": "/correct"}
	ct.aliasArgs(args2)
	if args2["file_path"] != "/correct" {
		t.Errorf("canonical value should be preserved, got %v", args2["file_path"])
	}

	// "glob" should be remapped to "pattern"
	args3 := map[string]any{"glob": "**/*.go"}
	ct.aliasArgs(args3)
	if args3["pattern"] != "**/*.go" {
		t.Errorf("expected pattern=**/*.go, got %v", args3["pattern"])
	}
}

func TestCoercingTool_Run_NonMapArgs(t *testing.T) {
	ct := makeCoercingTool(t)

	// Non-map args: coercion is skipped, delegation attempted (may panic with nil ctx)
	// We just verify it doesn't crash before attempting delegation.
	func() {
		defer func() { recover() }() //nolint:errcheck
		ct.Run(nil, "not-a-map")     //nolint:errcheck
	}()
}

func TestAliasArgs_CommonLLMMistakes(t *testing.T) {
	ct := &coercingTool{
		intProps:  map[string]bool{},
		boolProps: map[string]bool{},
		aliases: map[string]string{
			"type":    "agent",
			"prompt":  "task",
			"message": "task",
			"items":   "tasks",
			"steps":   "chain",
		},
	}

	tests := []struct {
		name        string
		input       map[string]any
		wantKey     string
		wantValue   any
		wantRemoved string
	}{
		{
			name:        "type to agent",
			input:       map[string]any{"type": "worker"},
			wantKey:     "agent",
			wantValue:   "worker",
			wantRemoved: "type",
		},
		{
			name:        "prompt to task",
			input:       map[string]any{"prompt": "do something"},
			wantKey:     "task",
			wantValue:   "do something",
			wantRemoved: "prompt",
		},
		{
			name:        "message to task",
			input:       map[string]any{"message": "urgent task"},
			wantKey:     "task",
			wantValue:   "urgent task",
			wantRemoved: "message",
		},
		{
			name:        "canonical name preserved over alias",
			input:       map[string]any{"agent": "correct", "type": "wrong"},
			wantKey:     "agent",
			wantValue:   "correct", // canonical should win
			wantRemoved: "type",    // alias should still be removed even if canonical exists
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ct.aliasArgs(tt.input)
			if v, ok := tt.input[tt.wantKey].(string); !ok || v != tt.wantValue.(string) {
				t.Errorf("aliasArgs: %s = %v, want %v", tt.wantKey, tt.input[tt.wantKey], tt.wantValue)
			}
			// Check alias was removed
			if _, exists := tt.input[tt.wantRemoved]; exists {
				t.Errorf("aliasArgs: %s should be removed but still exists", tt.wantRemoved)
			}
		})
	}
}

// TestTryCoerce characterizes every branch of tryCoerce: which value kinds are
// converted for which property class, and which are declined (nil) so the
// caller leaves the original value in place.
func TestTryCoerce(t *testing.T) {
	t.Parallel()

	c := &coercingTool{
		intProps:  map[string]bool{"n": true},
		boolProps: map[string]bool{"b": true},
		jsonProps: map[string]bool{"j": true},
	}

	tests := []struct {
		name string
		val  any
		path string
		want any
	}{
		// Strings against an integer property.
		{"string int", "42", "n", float64(42)},
		{"string negative int", "-7", "n", float64(-7)},
		{"string float falls back to ParseFloat", "3.5", "n", 3.5},
		{"string exponent falls back to ParseFloat", "1e3", "n", float64(1000)},
		{"string non-numeric declined", "abc", "n", nil},
		{"string empty declined", "", "n", nil},

		// Strings against a boolean property.
		{"string true", "true", "b", true},
		{"string 1 is bool true", "1", "b", true},
		{"string False", "False", "b", false},
		{"string non-bool declined", "yes", "b", nil},

		// Strings against a JSON property.
		{"stringified array", `[1,2]`, "j", []any{float64(1), float64(2)}},
		{"stringified object", `{"a":1}`, "j", map[string]any{"a": float64(1)}},
		{"stringified array with surrounding space", "  [1]  ", "j", []any{float64(1)}},
		{"malformed JSON array declined", `[1,`, "j", nil},
		{"unbracketed JSON declined", `1,2`, "j", nil},
		{"mismatched brackets declined", `[1}`, "j", nil},

		// Strings against an unknown property.
		{"unknown path declined", "42", "other", nil},

		// Numbers.
		{"float64 always declined", float64(1), "n", nil},
		{"float64 declined for unknown path", float64(1), "other", nil},
		{"int to float64", int(5), "n", float64(5)},
		{"int32 to float64", int32(5), "n", float64(5)},
		{"int64 to float64", int64(5), "n", float64(5)},
		{"float32 to float64", float32(2.5), "n", float64(2.5)},
		{"int declined for non-int prop", int(5), "b", nil},
		{"json.Number to float64", json.Number("12"), "n", float64(12)},
		{"json.Number invalid declined", json.Number("nope"), "n", nil},
		{"json.Number declined for non-int prop", json.Number("12"), "b", nil},

		// Everything else.
		{"bool declined", true, "b", nil},
		{"nil declined", nil, "n", nil},
		{"slice declined", []any{1}, "j", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := c.tryCoerce(tt.val, tt.path)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("tryCoerce(%#v, %q) mismatch (-want +got):\n%s", tt.val, tt.path, diff)
			}
		})
	}
}

// TestTryCoerce_PropClassPrecedence pins the else-if ordering: a path listed in
// more than one class is coerced as an integer first, then bool, then JSON.
func TestTryCoerce_PropClassPrecedence(t *testing.T) {
	t.Parallel()

	c := &coercingTool{
		intProps:  map[string]bool{"x": true},
		boolProps: map[string]bool{"x": true, "y": true},
		jsonProps: map[string]bool{"x": true, "y": true},
	}

	// "1" parses as both an int and a bool; intProps wins.
	if got := c.tryCoerce("1", "x"); got != float64(1) {
		t.Errorf(`tryCoerce("1", "x") = %#v, want float64(1)`, got)
	}
	// "[1]" is valid JSON but not a bool; boolProps still shadows jsonProps, so
	// the value is declined rather than parsed.
	if got := c.tryCoerce("[1]", "y"); got != nil {
		t.Errorf(`tryCoerce("[1]", "y") = %#v, want nil`, got)
	}
}

// TestCoerceArrayItem characterizes the recursive array walk: which schema
// paths reach an array element, and what happens to elements the walk cannot
// name. Written against the existing behavior, quirks included.
func TestCoerceArrayItem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tool *coercingTool
		in   map[string]any
		want map[string]any
	}{
		{
			name: "array item properties match the parent.$ path",
			tool: &coercingTool{intProps: map[string]bool{"tasks.$.depth": true}},
			in:   map[string]any{"tasks": []any{map[string]any{"depth": "5"}}},
			want: map[string]any{"tasks": []any{map[string]any{"depth": float64(5)}}},
		},
		{
			name: "array item properties also match the plain dotted path",
			tool: &coercingTool{intProps: map[string]bool{"tasks.depth": true}},
			in:   map[string]any{"tasks": []any{map[string]any{"depth": "5"}}},
			want: map[string]any{"tasks": []any{map[string]any{"depth": float64(5)}}},
		},
		{
			name: "bool array item property",
			tool: &coercingTool{boolProps: map[string]bool{"tasks.$.on": true}},
			in:   map[string]any{"tasks": []any{map[string]any{"on": "true"}}},
			want: map[string]any{"tasks": []any{map[string]any{"on": true}}},
		},
		{
			name: "unregistered array item property is left alone",
			tool: &coercingTool{intProps: map[string]bool{"other": true}},
			in:   map[string]any{"tasks": []any{map[string]any{"depth": "5"}}},
			want: map[string]any{"tasks": []any{map[string]any{"depth": "5"}}},
		},
		{
			name: "every element of the array is visited",
			tool: &coercingTool{intProps: map[string]bool{"tasks.$.depth": true}},
			in: map[string]any{"tasks": []any{
				map[string]any{"depth": "1"},
				map[string]any{"depth": "2"},
				map[string]any{"depth": "3"},
			}},
			want: map[string]any{"tasks": []any{
				map[string]any{"depth": float64(1)},
				map[string]any{"depth": float64(2)},
				map[string]any{"depth": float64(3)},
			}},
		},
		{
			name: "scalar array elements are untouched without a json prop",
			tool: &coercingTool{intProps: map[string]bool{"nums": true}},
			in:   map[string]any{"nums": []any{"1", "2"}},
			want: map[string]any{"nums": []any{"1", "2"}},
		},
		{
			name: "a stringified object inside an array is parsed via the array's json prop",
			tool: &coercingTool{jsonProps: map[string]bool{"tasks": true}},
			in:   map[string]any{"tasks": []any{`{"a":1}`}},
			want: map[string]any{"tasks": []any{map[string]any{"a": float64(1)}}},
		},
		{
			name: "an element parsed from a string is then walked as an object",
			tool: &coercingTool{
				jsonProps: map[string]bool{"tasks": true},
				intProps:  map[string]bool{"tasks.$.depth": true},
			},
			in:   map[string]any{"tasks": []any{`{"depth":"5"}`}},
			want: map[string]any{"tasks": []any{map[string]any{"depth": float64(5)}}},
		},
		{
			name: "arrays of arrays reuse the parent path for the inner elements",
			tool: &coercingTool{intProps: map[string]bool{"grid.$.depth": true}},
			in:   map[string]any{"grid": []any{[]any{map[string]any{"depth": "7"}}}},
			want: map[string]any{"grid": []any{[]any{map[string]any{"depth": float64(7)}}}},
		},
		{
			name: "an array nested under an array item property is walked",
			tool: &coercingTool{intProps: map[string]bool{"tasks.steps.$.n": true}},
			in: map[string]any{"tasks": []any{
				map[string]any{"steps": []any{map[string]any{"n": "9"}}},
			}},
			want: map[string]any{"tasks": []any{
				map[string]any{"steps": []any{map[string]any{"n": float64(9)}}},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.tool.coerceArgs(tt.in)
			if diff := cmp.Diff(tt.want, tt.in); diff != "" {
				t.Errorf("coerceArgs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestCoerceArrayItem_ObjectNestedInArrayItem pins a real quirk: when an array
// element holds a nested object, the walk recurses with the PARENT's key
// instead of iterating the child's own keys. So only a child field that
// happens to repeat the parent key name is ever reached.
func TestCoerceArrayItem_ObjectNestedInArrayItem(t *testing.T) {
	t.Parallel()

	t.Run("a differently named child field is never visited", func(t *testing.T) {
		t.Parallel()
		c := &coercingTool{intProps: map[string]bool{
			"depth": true, "opts.depth": true, "tasks.opts.depth": true, "tasks.$.opts.depth": true,
		}}
		m := map[string]any{"tasks": []any{
			map[string]any{"opts": map[string]any{"depth": "5"}},
		}}
		c.coerceArgs(m)

		opts := m["tasks"].([]any)[0].(map[string]any)["opts"].(map[string]any)
		if opts["depth"] != "5" {
			t.Errorf("depth = %#v, want the untouched string \"5\"; "+
				"the nested-object recursion now reaches differently named fields", opts["depth"])
		}
	})

	t.Run("a child field repeating the parent key name is visited", func(t *testing.T) {
		t.Parallel()
		c := &coercingTool{intProps: map[string]bool{"opts": true}}
		m := map[string]any{"tasks": []any{
			map[string]any{"opts": map[string]any{"opts": "5"}},
		}}
		c.coerceArgs(m)

		opts := m["tasks"].([]any)[0].(map[string]any)["opts"].(map[string]any)
		if opts["opts"] != float64(5) {
			t.Errorf("opts.opts = %#v, want float64(5)", opts["opts"])
		}
	})
}

// TestCoerceArrayItem_BothPathsRegistered pins the double-coercion order: the
// dotted path is applied first and the parent.$ path second, and the second
// pass re-reads the ORIGINAL value rather than the result of the first. When
// the two paths sit in different property classes the parent.$ class wins.
func TestCoerceArrayItem_BothPathsRegistered(t *testing.T) {
	t.Parallel()

	c := &coercingTool{
		intProps:  map[string]bool{"tasks.flag": true},
		boolProps: map[string]bool{"tasks.$.flag": true},
	}
	m := map[string]any{"tasks": []any{map[string]any{"flag": "1"}}}
	c.coerceArgs(m)

	got := m["tasks"].([]any)[0].(map[string]any)["flag"]
	if got != true {
		t.Errorf("flag = %#v (%T), want bool true: the parent.$ pass runs second and overwrites", got, got)
	}
}

// TestCoerceValueAtKey_NestedObject covers the top-level object recursion:
// a nested map is walked key by key, and each key is matched against the bare
// property name that collectFromSchema also registers for nested properties.
func TestCoerceValueAtKey_NestedObject(t *testing.T) {
	t.Parallel()

	c := &coercingTool{
		intProps:  map[string]bool{"depth": true},
		boolProps: map[string]bool{"verbose": true},
	}
	m := map[string]any{"opts": map[string]any{
		"depth":   "5",
		"verbose": "true",
		"name":    "keep me",
	}}
	c.coerceArgs(m)

	want := map[string]any{"opts": map[string]any{
		"depth":   float64(5),
		"verbose": true,
		"name":    "keep me",
	}}
	if diff := cmp.Diff(want, m); diff != "" {
		t.Errorf("coerceArgs mismatch (-want +got):\n%s", diff)
	}
}
