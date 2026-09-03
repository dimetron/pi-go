package provider

// Goldens pinning the Anthropic translation layer across the cognitive-
// complexity refactor of anthropic.go.
//
// Every literal in this file was captured by running the cases against the
// PRE-refactor source (extracted with `git archive HEAD` into a scratch tree),
// not by reading back the refactored code. They therefore assert that the
// refactor is a no-op rather than that the new code agrees with itself.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

// ---------------------------------------------------------------------------
// Tool schema conversion: antToolSchemas / antSchemaFields and the two
// converters built on them.
// ---------------------------------------------------------------------------

type cogBToolCase struct {
	name  string
	tools []*genai.Tool
	// want is the exact JSON the emitted tool list marshals to. The standard
	// and beta converters emit byte-identical wire schemas, so one literal
	// serves both; the test asserts that equality explicitly.
	want string
}

func cogBToolCases() []cogBToolCase {
	typed := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"path":      {Type: "string", Description: "file path"},
			"max_lines": {Type: "integer"},
		},
		Required: []string{"path"},
	}

	return []cogBToolCase{
		{name: "nil tools slice", tools: nil, want: `null`},
		{name: "empty tools slice", tools: []*genai.Tool{}, want: `null`},
		{name: "nil tool entry", tools: []*genai.Tool{nil}, want: `null`},
		{name: "nil function declarations", tools: []*genai.Tool{{}}, want: `null`},
		{
			name:  "empty function declarations",
			tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{}}},
			want:  `null`,
		},
		{
			name:  "nil declaration entry",
			tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{nil}}},
			want:  `null`,
		},
		{
			name: "no parameters yields empty object schema",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "ping", Description: "no args",
			}),
			want: `[{"input_schema":{"properties":{},"type":"object"},"name":"ping","description":"no args"}]`,
		},
		{
			name:  "empty description is still emitted",
			tools: cogBTools(&genai.FunctionDeclaration{Name: "bare"}),
			want:  `[{"input_schema":{"properties":{},"type":"object"},"name":"bare","description":""}]`,
		},
		{
			name: "required and optional properties",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name:        "read_file",
				Description: "Read a file",
				ParametersJsonSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":   map[string]any{"type": "string", "description": "file path"},
						"offset": map[string]any{"type": "integer"},
					},
					"required": []any{"path"},
				},
			}),
			want: `[{"input_schema":{"properties":{"offset":{"type":"integer"},"path":{"description":"file path","type":"string"}},"required":["path"],"type":"object"},"name":"read_file","description":"Read a file"}]`,
		},
		{
			name: "properties without required omits required",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "opt_only",
				ParametersJsonSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"q": map[string]any{"type": "string"}},
				},
			}),
			want: `[{"input_schema":{"properties":{"q":{"type":"string"}},"type":"object"},"name":"opt_only","description":""}]`,
		},
		{
			// An empty JSON array is distinct from an absent one: it emits
			// "required":[] where an absent key emits nothing at all.
			name: "empty required array survives as empty array",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "req_empty",
				ParametersJsonSchema: map[string]any{
					"properties": map[string]any{"q": map[string]any{"type": "string"}},
					"required":   []any{},
				},
			}),
			want: `[{"input_schema":{"properties":{"q":{"type":"string"}},"required":[],"type":"object"},"name":"req_empty","description":""}]`,
		},
		{
			name: "non-string required entries are dropped",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "req_mixed",
				ParametersJsonSchema: map[string]any{
					"properties": map[string]any{"a": map[string]any{"type": "string"}},
					"required":   []any{"a", 42, nil, "b", true},
				},
			}),
			want: `[{"input_schema":{"properties":{"a":{"type":"string"}},"required":["a","b"],"type":"object"},"name":"req_mixed","description":""}]`,
		},
		{
			// An array carrying no usable string entries still emits an empty
			// array, not an absent key. This is the case that decides whether
			// the required-list reader can be shared with the Ollama
			// converter: ToolInputSchemaParam.Required is `omitzero`, so a nil
			// slice and a non-nil empty slice are different on the wire.
			name: "required with only non-string entries emits empty array",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "req_junk",
				ParametersJsonSchema: map[string]any{
					"properties": map[string]any{"a": map[string]any{"type": "string"}},
					"required":   []any{42, nil, true, 3.5},
				},
			}),
			want: `[{"input_schema":{"properties":{"a":{"type":"string"}},"required":[],"type":"object"},"name":"req_junk","description":""}]`,
		},
		{
			name: "required as a bare string is ignored",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "req_str",
				ParametersJsonSchema: map[string]any{
					"properties": map[string]any{"a": map[string]any{"type": "string"}},
					"required":   "a",
				},
			}),
			want: `[{"input_schema":{"properties":{"a":{"type":"string"}},"type":"object"},"name":"req_str","description":""}]`,
		},
		{
			name: "required as a map is ignored",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "req_map",
				ParametersJsonSchema: map[string]any{
					"properties": map[string]any{"a": map[string]any{"type": "string"}},
					"required":   map[string]any{"a": true},
				},
			}),
			want: `[{"input_schema":{"properties":{"a":{"type":"string"}},"type":"object"},"name":"req_map","description":""}]`,
		},
		{
			// A []string (rather than []any) fails the assertion, so required
			// is dropped entirely. Preserved quirk, pinned deliberately.
			name: "required as []string is ignored",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "req_strings",
				ParametersJsonSchema: map[string]any{
					"properties": map[string]any{"a": map[string]any{"type": "string"}},
					"required":   []string{"a"},
				},
			}),
			want: `[{"input_schema":{"properties":{"a":{"type":"string"}},"type":"object"},"name":"req_strings","description":""}]`,
		},
		{
			name: "non-map properties fall back to empty object",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "weird",
				ParametersJsonSchema: map[string]any{
					"properties": "not-a-map",
					"required":   []any{"a"},
				},
			}),
			want: `[{"input_schema":{"properties":{},"required":["a"],"type":"object"},"name":"weird","description":""}]`,
		},
		{
			name: "empty schema map",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "empty_schema", ParametersJsonSchema: map[string]any{},
			}),
			want: `[{"input_schema":{"properties":{},"type":"object"},"name":"empty_schema","description":""}]`,
		},
		{
			// schemaToMap returns nil when the value cannot be marshaled.
			name: "unmarshalable schema falls back to empty object",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "bad_schema", ParametersJsonSchema: make(chan int),
			}),
			want: `[{"input_schema":{"properties":{},"type":"object"},"name":"bad_schema","description":""}]`,
		},
		{
			name: "scalar schema falls back to empty object",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "scalar_schema", ParametersJsonSchema: "just-a-string",
			}),
			want: `[{"input_schema":{"properties":{},"type":"object"},"name":"scalar_schema","description":""}]`,
		},
		{
			name: "nested object properties pass through verbatim",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name:        "nested",
				Description: "nested object properties",
				ParametersJsonSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"filter": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"since": map[string]any{"type": "string"},
								"limit": map[string]any{"type": "integer"},
							},
							"required": []any{"since"},
						},
					},
					"required": []any{"filter"},
				},
			}),
			want: `[{"input_schema":{"properties":{"filter":{"properties":{"limit":{"type":"integer"},"since":{"type":"string"}},"required":["since"],"type":"object"}},"required":["filter"],"type":"object"},"name":"nested","description":"nested object properties"}]`,
		},
		{
			name: "arrays and enums pass through verbatim",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name:        "arr_enum",
				Description: "arrays and enums",
				ParametersJsonSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"tags": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "string"},
						},
						"mode": map[string]any{
							"type": "string",
							"enum": []any{"fast", "slow"},
						},
					},
					"required": []any{"tags", "mode"},
				},
			}),
			want: `[{"input_schema":{"properties":{"mode":{"enum":["fast","slow"],"type":"string"},"tags":{"items":{"type":"string"},"type":"array"}},"required":["tags","mode"],"type":"object"},"name":"arr_enum","description":"arrays and enums"}]`,
		},
		{
			// The tools registry hands over a typed *jsonschema.Schema, which
			// only survives via schemaToMap's marshal round trip.
			name: "typed jsonschema.Schema",
			tools: cogBTools(&genai.FunctionDeclaration{
				Name: "typed", Description: "typed schema", ParametersJsonSchema: typed,
			}),
			want: `[{"input_schema":{"properties":{"max_lines":{"type":"integer"},"path":{"description":"file path","type":"string"}},"required":["path"],"type":"object"},"name":"typed","description":"typed schema"}]`,
		},
		{
			name: "multiple tools and declarations keep source order",
			tools: []*genai.Tool{
				{FunctionDeclarations: []*genai.FunctionDeclaration{
					{Name: "first"},
					nil,
					{Name: "second", ParametersJsonSchema: map[string]any{
						"properties": map[string]any{"x": map[string]any{"type": "number"}},
						"required":   []any{"x"},
					}},
				}},
				nil,
				{FunctionDeclarations: nil},
				{FunctionDeclarations: []*genai.FunctionDeclaration{
					{Name: "third", Description: "third tool"},
				}},
			},
			want: `[{"input_schema":{"properties":{},"type":"object"},"name":"first","description":""},{"input_schema":{"properties":{"x":{"type":"number"}},"required":["x"],"type":"object"},"name":"second","description":""},{"input_schema":{"properties":{},"type":"object"},"name":"third","description":"third tool"}]`,
		},
	}
}

func cogBTools(decls ...*genai.FunctionDeclaration) []*genai.Tool {
	return []*genai.Tool{{FunctionDeclarations: decls}}
}

func cogBMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

func TestCogProviderBToolConverterGoldens(t *testing.T) {
	for _, tc := range cogBToolCases() {
		t.Run(tc.name, func(t *testing.T) {
			gotStd := cogBMarshal(t, antGenaiToolsToAnthropic(tc.tools))
			if gotStd != tc.want {
				t.Errorf("antGenaiToolsToAnthropic:\n got %s\nwant %s", gotStd, tc.want)
			}
			gotBeta := cogBMarshal(t, antGenaiToolsToBetaAnthropic(tc.tools))
			if gotBeta != tc.want {
				t.Errorf("antGenaiToolsToBetaAnthropic:\n got %s\nwant %s", gotBeta, tc.want)
			}
			// The shared core is only legitimate if the two formats really do
			// emit the same wire schema for every declaration shape.
			if gotStd != gotBeta {
				t.Errorf("standard and beta schemas diverge:\n std  %s\n beta %s", gotStd, gotBeta)
			}
		})
	}
}

// TestCogProviderBSchemaFields pins the nil-versus-empty distinctions that the
// marshaled goldens above cannot see: Properties is never nil, and Required is
// nil only when the schema carries no JSON array.
func TestCogProviderBSchemaFields(t *testing.T) {
	tests := []struct {
		name         string
		schema       any
		wantProps    map[string]any
		wantRequired []string
		wantReqNil   bool
	}{
		{
			name: "nil schema", schema: nil,
			wantProps: map[string]any{}, wantReqNil: true,
		},
		{
			name: "unreadable schema", schema: make(chan int),
			wantProps: map[string]any{}, wantReqNil: true,
		},
		{
			name:      "required absent",
			schema:    map[string]any{"properties": map[string]any{"a": "x"}},
			wantProps: map[string]any{"a": "x"}, wantReqNil: true,
		},
		{
			name:      "required empty array is non-nil",
			schema:    map[string]any{"required": []any{}},
			wantProps: map[string]any{}, wantRequired: []string{},
		},
		{
			name:      "required filters non-strings",
			schema:    map[string]any{"required": []any{"a", 1, "b"}},
			wantProps: map[string]any{}, wantRequired: []string{"a", "b"},
		},
		{
			// Non-nil but empty: emits "required": [] rather than omitting it.
			name:      "required with only non-strings is non-nil empty",
			schema:    map[string]any{"required": []any{1, true}},
			wantProps: map[string]any{}, wantRequired: []string{},
		},
		{
			name:      "required as a bare string is nil",
			schema:    map[string]any{"required": "a"},
			wantProps: map[string]any{}, wantReqNil: true,
		},
		{
			name:      "required as []string is nil",
			schema:    map[string]any{"required": []string{"a"}},
			wantProps: map[string]any{}, wantReqNil: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			props, required := antSchemaFields(tc.schema)
			if props == nil {
				t.Fatal("properties must never be nil")
			}
			if got := cogBMarshal(t, props); got != cogBMarshal(t, tc.wantProps) {
				t.Errorf("properties = %s, want %s", got, cogBMarshal(t, tc.wantProps))
			}
			if tc.wantReqNil {
				if required != nil {
					t.Errorf("required = %v, want nil", required)
				}
				return
			}
			if required == nil {
				t.Fatal("required = nil, want non-nil")
			}
			if got := cogBMarshal(t, required); got != cogBMarshal(t, tc.wantRequired) {
				t.Errorf("required = %s, want %s", got, cogBMarshal(t, tc.wantRequired))
			}
		})
	}
}

// TestCogProviderBToolSchemas pins the flattening step on its own: which
// declarations survive, in which order.
func TestCogProviderBToolSchemas(t *testing.T) {
	got := antToolSchemas([]*genai.Tool{
		nil,
		{FunctionDeclarations: nil},
		{FunctionDeclarations: []*genai.FunctionDeclaration{}},
		{FunctionDeclarations: []*genai.FunctionDeclaration{nil, {Name: "a"}, nil, {Name: "b"}}},
		{FunctionDeclarations: []*genai.FunctionDeclaration{{Name: "c", Description: "third"}}},
	})
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (%+v)", len(got), got)
	}
	wantNames := []string{"a", "b", "c"}
	for i, want := range wantNames {
		if got[i].name != want {
			t.Errorf("[%d].name = %q, want %q", i, got[i].name, want)
		}
	}
	if got[2].description != "third" {
		t.Errorf("[2].description = %q, want %q", got[2].description, "third")
	}
	if antToolSchemas(nil) != nil {
		t.Error("antToolSchemas(nil) must be nil")
	}
}

// ---------------------------------------------------------------------------
// Model-name resolution
// ---------------------------------------------------------------------------

func TestCogProviderBResolveModelName(t *testing.T) {
	tests := []struct {
		name       string
		configured string
		requested  string
		want       string
	}{
		{"configured only", "claude-sonnet-4-6", "", "claude-sonnet-4-6"},
		{"request overrides", "claude-sonnet-4-6", "claude-opus-4-7", "claude-opus-4-7"},
		{"provider alias request keeps configured", "claude-sonnet-4-6", "anthropic", "claude-sonnet-4-6"},
		{"both empty falls back", "", "", "claude-sonnet-5"},
		{"both alias falls back", "anthropic", "anthropic", "claude-sonnet-5"},
		{"empty configured with alias request falls back", "", "anthropic", "claude-sonnet-5"},
		{"empty configured with real request", "", "claude-opus-4-7", "claude-opus-4-7"},
		{"alias configured with real request", "anthropic", "claude-opus-4-7", "claude-opus-4-7"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := antResolveModelName(tc.configured, tc.requested); got != tc.want {
				t.Errorf("antResolveModelName(%q, %q) = %q, want %q",
					tc.configured, tc.requested, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Outgoing request bodies: buildParams / buildBetaParams as reached through
// GenerateContent's routing.
// ---------------------------------------------------------------------------

func cogBCaptureRequest(t *testing.T, modelName, reqModel, thinking string, opts *LLMOptions, config *genai.GenerateContentConfig) string {
	t.Helper()
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "m", "type": "message", "role": "assistant", "model": "x",
			"content":     []map[string]any{{"type": "text", "text": "ok"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, modelName, "sk-test", srv.URL, thinking, opts)
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{
		Model:    reqModel,
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
		Config:   config,
	}
	for _, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
	}
	// Re-marshal through map[string]any so key order is canonical.
	var m map[string]any
	if err := json.Unmarshal(captured, &m); err != nil {
		t.Fatalf("unmarshal request body: %v (%s)", err, captured)
	}
	return cogBMarshal(t, m)
}

func TestCogProviderBRequestBodyGoldens(t *testing.T) {
	toolsCfg := func() *genai.GenerateContentConfig {
		return &genai.GenerateContentConfig{
			SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: "be brief"}}},
			Tools: []*genai.Tool{{FunctionDeclarations: []*genai.FunctionDeclaration{
				{Name: "read_file", Description: "Read a file", ParametersJsonSchema: map[string]any{
					"type":       "object",
					"properties": map[string]any{"path": map[string]any{"type": "string"}},
					"required":   []any{"path"},
				}},
			}}},
		}
	}

	tests := []struct {
		name       string
		modelName  string
		reqModel   string
		thinking   string
		opts       *LLMOptions
		config     *genai.GenerateContentConfig
		wantBody   string
		wantCached bool
	}{
		{
			name: "plain request", modelName: "claude-sonnet-4-6", thinking: "none",
			wantBody: `{"max_tokens":8192,"messages":[{"content":[{"cache_control":{"type":"ephemeral"},"text":"hi","type":"text"}],"role":"user"}],"model":"claude-sonnet-4-6"}`,
		},
		{
			name: "request model override", modelName: "claude-sonnet-4-6", reqModel: "claude-opus-4-7", thinking: "none",
			wantBody: `{"max_tokens":8192,"messages":[{"content":[{"cache_control":{"type":"ephemeral"},"text":"hi","type":"text"}],"role":"user"}],"model":"claude-opus-4-7"}`,
		},
		{
			name: "provider alias in request", modelName: "claude-sonnet-4-6", reqModel: "anthropic", thinking: "none",
			wantBody: `{"max_tokens":8192,"messages":[{"content":[{"cache_control":{"type":"ephemeral"},"text":"hi","type":"text"}],"role":"user"}],"model":"claude-sonnet-4-6"}`,
		},
		{
			name: "empty model falls back", modelName: "", thinking: "none",
			wantBody: `{"max_tokens":8192,"messages":[{"content":[{"cache_control":{"type":"ephemeral"},"text":"hi","type":"text"}],"role":"user"}],"model":"claude-sonnet-5"}`,
		},
		{
			name: "alias model falls back", modelName: "anthropic", reqModel: "anthropic", thinking: "none",
			wantBody: `{"max_tokens":8192,"messages":[{"content":[{"cache_control":{"type":"ephemeral"},"text":"hi","type":"text"}],"role":"user"}],"model":"claude-sonnet-5"}`,
		},
		{
			// Thinking raises max_tokens to 16384 and adds the adaptive block.
			name: "thinking high", modelName: "claude-sonnet-4-6", thinking: "high",
			wantBody: `{"max_tokens":16384,"messages":[{"content":[{"cache_control":{"type":"ephemeral"},"text":"hi","type":"text"}],"role":"user"}],"model":"claude-sonnet-4-6","thinking":{"display":"summarized","type":"adaptive"}}`,
		},
		{
			name: "unrecognized thinking level is off", modelName: "claude-sonnet-4-6", thinking: "bogus",
			wantBody: `{"max_tokens":8192,"messages":[{"content":[{"cache_control":{"type":"ephemeral"},"text":"hi","type":"text"}],"role":"user"}],"model":"claude-sonnet-4-6"}`,
		},
		{
			name: "system prompt and tools", modelName: "claude-sonnet-4-6", thinking: "none", config: toolsCfg(),
			wantBody: `{"max_tokens":8192,"messages":[{"content":[{"cache_control":{"type":"ephemeral"},"text":"hi","type":"text"}],"role":"user"}],"model":"claude-sonnet-4-6","system":[{"cache_control":{"type":"ephemeral"},"text":"be brief","type":"text"}],"tools":[{"cache_control":{"type":"ephemeral"},"description":"Read a file","input_schema":{"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"},"name":"read_file"}]}`,
		},
		{
			name: "prompt caching disabled", modelName: "claude-sonnet-4-6", thinking: "none",
			opts: &LLMOptions{DisablePromptCaching: true}, config: toolsCfg(),
			wantBody: `{"max_tokens":8192,"messages":[{"content":[{"text":"hi","type":"text"}],"role":"user"}],"model":"claude-sonnet-4-6","system":[{"text":"be brief","type":"text"}],"tools":[{"description":"Read a file","input_schema":{"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"},"name":"read_file"}]}`,
		},
		{
			// An empty Tools slice must not produce a "tools" key.
			name: "empty tools slice", modelName: "claude-sonnet-4-6", thinking: "none",
			config:   &genai.GenerateContentConfig{Tools: []*genai.Tool{}},
			wantBody: `{"max_tokens":8192,"messages":[{"content":[{"cache_control":{"type":"ephemeral"},"text":"hi","type":"text"}],"role":"user"}],"model":"claude-sonnet-4-6"}`,
		},
		{
			// Advisor path: the advisor tool leads the Tools slice and the
			// converted genai tools follow it.
			name: "beta advisor with thinking and caching", modelName: "claude-sonnet-4-6", thinking: "high",
			opts:     &LLMOptions{AdvisorModel: "claude-opus-4-7", AdvisorMaxUses: 3, AdvisorCaching: true},
			config:   toolsCfg(),
			wantBody: `{"max_tokens":16384,"messages":[{"content":[{"cache_control":{"type":"ephemeral"},"text":"hi","type":"text"}],"role":"user"}],"model":"claude-sonnet-4-6","system":[{"cache_control":{"type":"ephemeral"},"text":"be brief","type":"text"}],"thinking":{"display":"summarized","type":"adaptive"},"tools":[{"caching":{"type":"ephemeral"},"max_uses":3,"model":"claude-opus-4-7","name":"advisor","type":"advisor_20260301"},{"cache_control":{"type":"ephemeral"},"description":"Read a file","input_schema":{"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"},"name":"read_file"}]}`,
		},
		{
			name: "beta advisor without caching", modelName: "claude-sonnet-4-6", thinking: "none",
			opts:     &LLMOptions{AdvisorModel: "claude-opus-4-7", DisablePromptCaching: true},
			config:   toolsCfg(),
			wantBody: `{"max_tokens":8192,"messages":[{"content":[{"text":"hi","type":"text"}],"role":"user"}],"model":"claude-sonnet-4-6","system":[{"text":"be brief","type":"text"}],"tools":[{"model":"claude-opus-4-7","name":"advisor","type":"advisor_20260301"},{"description":"Read a file","input_schema":{"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"},"name":"read_file"}]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := cogBCaptureRequest(t, tc.modelName, tc.reqModel, tc.thinking, tc.opts, tc.config)
			if got != tc.wantBody {
				t.Errorf("request body:\n got %s\nwant %s", got, tc.wantBody)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// antRunNonStreamingBeta: block -> part conversion
// ---------------------------------------------------------------------------

type cogBPart struct {
	Text   string         `json:"text,omitempty"`
	FCName string         `json:"fc_name,omitempty"`
	FCID   string         `json:"fc_id,omitempty"`
	FCArgs map[string]any `json:"fc_args,omitempty"`
	FCNil  bool           `json:"fc_nil_args,omitempty"`
}

func cogBParts(parts []*genai.Part) []cogBPart {
	out := make([]cogBPart, 0, len(parts))
	for _, p := range parts {
		g := cogBPart{Text: p.Text}
		if p.FunctionCall != nil {
			g.FCName = p.FunctionCall.Name
			g.FCID = p.FunctionCall.ID
			g.FCArgs = p.FunctionCall.Args
			g.FCNil = p.FunctionCall.Args == nil
		}
		out = append(out, g)
	}
	return out
}

// TestCogProviderBBetaNonStreamingGolden drives every beta content-block shape
// through antRunNonStreamingBeta in one response: empty text and thinking are
// dropped, a tool_use identifying no tool is dropped, absent tool input leaves
// nil arguments, a redacted advisor result becomes its placeholder string, an
// empty advisor result is dropped, and an unhandled block type contributes
// nothing. Order is preserved.
func TestCogProviderBBetaNonStreamingGolden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_golden", "type": "message", "role": "assistant", "model": "claude-sonnet-4-6",
			"content": []map[string]any{
				{"type": "text", "text": "hello"},
				{"type": "text", "text": ""},
				{"type": "thinking", "thinking": "reasoning"},
				{"type": "thinking", "thinking": ""},
				{"type": "tool_use", "id": "toolu_1", "name": "bash", "input": map[string]any{"command": "ls"}},
				{"type": "tool_use", "id": "toolu_2", "name": "noargs"},
				{"type": "tool_use", "id": "toolu_3", "name": "nested", "input": map[string]any{
					"filter": map[string]any{"since": "yesterday", "limit": 3},
					"tags":   []any{"a", "b"},
				}},
				{"type": "advisor_tool_result", "tool_use_id": "advtool_1",
					"content": map[string]any{"type": "advisor_result", "text": "advice payload"}},
				{"type": "advisor_tool_result", "tool_use_id": "advtool_2",
					"content": map[string]any{"type": "advisor_redacted_result", "encrypted_content": "zzz"}},
				{"type": "advisor_tool_result", "tool_use_id": "advtool_3",
					"content": map[string]any{"type": "advisor_result", "text": ""}},
				{"type": "tool_use", "id": "", "name": "", "input": map[string]any{"x": 1}},
				{"type": "server_tool_use", "id": "srv_1", "name": "web_search"},
				{"type": "text", "text": "tail"},
			},
			"stop_reason": "tool_use",
			"usage": map[string]any{
				"input_tokens": 13, "output_tokens": 29,
				"cache_read_input_tokens": 5, "cache_creation_input_tokens": 2,
			},
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none",
		&LLMOptions{AdvisorModel: "claude-opus-4-7"})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{Contents: []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
	}}

	var last *model.LLMResponse
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
		last = resp
	}
	if last == nil || last.Content == nil {
		t.Fatal("no response")
	}

	// "noargs" carries an empty argument map rather than a nil one: a nil map
	// replays as tool_use.input null, which Anthropic rejects for every later
	// request in the conversation. See newFunctionCallPart.
	const wantParts = `[{"text":"hello"},{"text":"reasoning"},{"fc_name":"bash","fc_id":"toolu_1","fc_args":{"command":"ls"}},{"fc_name":"noargs","fc_id":"toolu_2"},{"fc_name":"nested","fc_id":"toolu_3","fc_args":{"filter":{"limit":3,"since":"yesterday"},"tags":["a","b"]}},{"text":"advice payload"},{"text":"[advisor result encrypted; will be decrypted on next turn]"},{"text":"tail"}]`
	if got := cogBMarshal(t, cogBParts(last.Content.Parts)); got != wantParts {
		t.Errorf("parts:\n got %s\nwant %s", got, wantParts)
	}

	// Note: the beta path reports raw input_tokens; it does not fold the cache
	// counters into the prompt count the way the streaming path does.
	const wantUsage = `{"cachedContentTokenCount":5,"candidatesTokenCount":29,"promptTokenCount":13}`
	if got := cogBMarshal(t, last.UsageMetadata); got != wantUsage {
		t.Errorf("usage:\n got %s\nwant %s", got, wantUsage)
	}
	if last.FinishReason != genai.FinishReasonStop {
		t.Errorf("FinishReason = %v, want STOP", last.FinishReason)
	}
	if last.Partial || !last.TurnComplete || last.Content.Role != "model" {
		t.Errorf("partial=%v turn=%v role=%q, want false/true/model",
			last.Partial, last.TurnComplete, last.Content.Role)
	}
}

// TestCogProviderBBetaNonStreamingError pins the early return when the API
// call fails: one error and no response.
func TestCogProviderBBetaNonStreamingError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"nope"}}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none",
		&LLMOptions{AdvisorModel: "claude-opus-4-7"})
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{Contents: []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
	}}

	var errCount, respCount int
	for resp, err := range llm.GenerateContent(ctx, req, false) {
		if err != nil {
			errCount++
		}
		if resp != nil {
			respCount++
		}
	}
	if errCount != 1 || respCount != 0 {
		t.Errorf("errCount=%d respCount=%d, want 1/0", errCount, respCount)
	}
}

// ---------------------------------------------------------------------------
// antHandleContentBlockDelta, via the standard streaming path
// ---------------------------------------------------------------------------

// TestCogProviderBStandardStreamingGolden pins the full yielded sequence of the
// standard streaming path: text deltas accumulate into the final text, a
// thinking delta is yielded under the "thinking" role, a signature_delta is
// ignored, an input_json_delta for an index with no tool_use header is dropped,
// and the final usage folds the cache counters into the prompt count.
func TestCogProviderBStandardStreamingGolden(t *testing.T) {
	events := []struct {
		name    string
		payload map[string]any
	}{
		{"message_start", map[string]any{"type": "message_start", "message": map[string]any{
			"id": "msg_s", "type": "message", "role": "assistant", "model": "m",
			"content": []any{}, "stop_reason": nil,
			"usage": map[string]any{"input_tokens": 7, "output_tokens": 0,
				"cache_read_input_tokens": 3, "cache_creation_input_tokens": 1},
		}}},
		{"content_block_start", map[string]any{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""}}},
		{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "Hel"}}},
		{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "lo"}}},
		{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "thinking_delta", "thinking": "hmm"}}},
		{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "signature_delta", "signature": "sig"}}},
		{"content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}},
		{"content_block_start", map[string]any{"type": "content_block_start", "index": 1,
			"content_block": map[string]any{"type": "tool_use", "id": "toolu_9", "name": "bash",
				"input": map[string]any{}}}},
		{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 1,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"cmd":`}}},
		{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 1,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `"ls"}`}}},
		{"content_block_delta", map[string]any{"type": "content_block_delta", "index": 5,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"orphan":1}`}}},
		{"content_block_stop", map[string]any{"type": "content_block_stop", "index": 1}},
		{"message_delta", map[string]any{"type": "message_delta",
			"delta": map[string]any{"stop_reason": "tool_use"},
			"usage": map[string]any{"output_tokens": 42, "cache_read_input_tokens": 9,
				"cache_creation_input_tokens": 0}}},
		{"message_stop", map[string]any{"type": "message_stop"}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		for _, e := range events {
			b, err := json.Marshal(e.payload)
			if err != nil {
				return
			}
			if _, err := w.Write([]byte("event: " + e.name + "\ndata: " + string(b) + "\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none", nil)
	if err != nil {
		t.Fatalf("NewAnthropic: %v", err)
	}
	req := &model.LLMRequest{Contents: []*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
	}}

	type rec struct {
		Role    string     `json:"role"`
		Partial bool       `json:"partial"`
		Turn    bool       `json:"turn"`
		Finish  string     `json:"finish,omitempty"`
		Parts   []cogBPart `json:"parts"`
		Usage   any        `json:"usage,omitempty"`
	}
	var got []rec
	for resp, err := range llm.GenerateContent(ctx, req, true) {
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		r := rec{Partial: resp.Partial, Turn: resp.TurnComplete, Finish: string(resp.FinishReason)}
		if resp.Content != nil {
			r.Role = resp.Content.Role
			r.Parts = cogBParts(resp.Content.Parts)
		}
		if resp.UsageMetadata != nil {
			r.Usage = resp.UsageMetadata
		}
		got = append(got, r)
	}

	const want = `[{"role":"model","partial":true,"turn":false,"parts":[{"text":"Hel"}]},{"role":"model","partial":true,"turn":false,"parts":[{"text":"lo"}]},{"role":"thinking","partial":true,"turn":false,"parts":[{"text":"hmm"}]},{"role":"model","partial":false,"turn":true,"finish":"STOP","parts":[{"text":"Hello"},{"fc_name":"bash","fc_id":"toolu_9","fc_args":{"cmd":"ls"}}],"usage":{"cachedContentTokenCount":9,"candidatesTokenCount":42,"promptTokenCount":17}}]`
	if g := cogBMarshal(t, got); g != want {
		t.Errorf("stream sequence:\n got %s\nwant %s", g, want)
	}
}

// TestCogProviderBStreamingStopsOnConsumerBreak pins the early-exit path of
// antHandleContentBlockDelta: once the consumer stops reading, no further
// events are yielded.
func TestCogProviderBStreamingStopsOnConsumerBreak(t *testing.T) {
	for _, deltaType := range []string{"text_delta", "thinking_delta"} {
		t.Run(deltaType, func(t *testing.T) {
			delta := map[string]any{"type": deltaType}
			if deltaType == "text_delta" {
				delta["text"] = "a"
			} else {
				delta["thinking"] = "a"
			}

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				flusher, ok := w.(http.Flusher)
				if !ok {
					return
				}
				for range 3 {
					b, err := json.Marshal(map[string]any{
						"type": "content_block_delta", "index": 0, "delta": delta,
					})
					if err != nil {
						return
					}
					if _, err := w.Write([]byte("event: content_block_delta\ndata: " + string(b) + "\n\n")); err != nil {
						return
					}
					flusher.Flush()
				}
			}))
			defer srv.Close()

			ctx := context.Background()
			llm, err := NewAnthropic(ctx, "claude-sonnet-4-6", "sk-test", srv.URL, "none", nil)
			if err != nil {
				t.Fatalf("NewAnthropic: %v", err)
			}
			req := &model.LLMRequest{Contents: []*genai.Content{
				{Role: "user", Parts: []*genai.Part{{Text: "hi"}}},
			}}

			n := 0
			for range llm.GenerateContent(ctx, req, true) {
				n++
				break
			}
			if n != 1 {
				t.Errorf("yielded %d responses after break, want 1", n)
			}
		})
	}
}
