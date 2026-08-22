package voicegemini

import (
	"encoding/json"
	"testing"
)

// These cases were captured against the pre-refactor sanitizeSchemaNode and are
// pasted in as literals, so they pin what the flattened version must reproduce
// exactly rather than agreeing with whatever it now does.
//
// The function guards a startup path that cannot be observed locally: a keyword
// Gemini rejects closes the live session with WebSocket 1007 before a single
// microphone byte is sent, so a silent change in what gets stripped only
// surfaces against a live model.
func TestSanitizeSchemaNodePinnedOutput(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		// --- recursion depth -------------------------------------------------
		{
			name: "recurses through nested properties to depth three",
			in:   `{"type":"object","additionalProperties":false,"properties":{"a":{"type":"object","additionalProperties":false,"properties":{"b":{"type":["null","string"],"additionalProperties":true}}}}}`,
			want: `{"properties":{"a":{"properties":{"b":{"nullable":true,"type":"string"}},"type":"object"}},"type":"object"}`,
		},
		{
			name: "recurses into items of items",
			in:   `{"type":"array","items":{"type":"array","items":{"type":"object","additionalProperties":false}}}`,
			want: `{"items":{"items":{"type":"object"},"type":"array"},"type":"array"}`,
		},
		{
			name: "recurses into anyOf members and their properties",
			in:   `{"anyOf":[{"type":["null","object"],"properties":{"a":{"additionalProperties":false}}},{"type":"string"}]}`,
			want: `{"anyOf":[{"nullable":true,"properties":{"a":{}},"type":"object"},{"type":"string"}]}`,
		},

		// --- the type-union collapse ----------------------------------------
		{
			name: "keeps the first non-null member of a multi-member union",
			in:   `{"type":["string","number","null"]}`,
			want: `{"nullable":true,"type":"string"}`,
		},
		{
			name: "an empty type array drops the type",
			in:   `{"type":[]}`,
			want: `{}`,
		},
		{
			name: "a non-string union member is kept verbatim",
			in:   `{"type":[123,"null"]}`,
			want: `{"nullable":true,"type":123}`,
		},
		{
			name: "an existing nullable survives a union without null",
			in:   `{"type":["string"],"nullable":false}`,
			want: `{"nullable":false,"type":"string"}`,
		},
		{
			name: "a union with null overwrites an existing nullable",
			in:   `{"type":["null","string"],"nullable":false}`,
			want: `{"nullable":true,"type":"string"}`,
		},
		{
			name: "a scalar type is left alone",
			in:   `{"type":"object"}`,
			want: `{"type":"object"}`,
		},
		{
			name: "a non-array non-string type is left alone",
			in:   `{"type":7}`,
			want: `{"type":7}`,
		},

		// --- containers that are not schema objects -------------------------
		// Quirks of the pre-refactor code, preserved deliberately: only the
		// exact shapes above are descended into, so anything else passes
		// through untouched, additionalProperties included.
		{
			name: "a non-object property value is not descended into",
			in:   `{"properties":{"a":"not-a-schema","b":[1,2]}}`,
			want: `{"properties":{"a":"not-a-schema","b":[1,2]}}`,
		},
		{
			name: "an array-valued items is not descended into",
			in:   `{"items":[{"additionalProperties":false}]}`,
			want: `{"items":[{"additionalProperties":false}]}`,
		},
		{
			name: "an object-valued anyOf is not descended into",
			in:   `{"anyOf":{"a":{"additionalProperties":false}}}`,
			want: `{"anyOf":{"a":{"additionalProperties":false}}}`,
		},
		{
			name: "a non-object anyOf member is skipped",
			in:   `{"anyOf":["x",{"additionalProperties":false}]}`,
			want: `{"anyOf":["x",{}]}`,
		},
		{
			name: "a non-object properties value is not descended into",
			in:   `{"properties":["a"]}`,
			want: `{"properties":["a"]}`,
		},

		// --- keywords the function knows nothing about ----------------------
		{
			name: "a $ref passes through untouched",
			in:   `{"$ref":"#/$defs/Foo","additionalProperties":false}`,
			want: `{"$ref":"#/$defs/Foo"}`,
		},
		{
			name: "$defs is not descended into",
			in:   `{"$defs":{"Foo":{"additionalProperties":false}},"$ref":"#/$defs/Foo"}`,
			want: `{"$defs":{"Foo":{"additionalProperties":false}},"$ref":"#/$defs/Foo"}`,
		},
		{
			name: "required and enum pass through",
			in:   `{"type":"object","required":["a"],"enum":["x","y"],"additionalProperties":false}`,
			want: `{"enum":["x","y"],"required":["a"],"type":"object"}`,
		},

		// --- empty and null shapes ------------------------------------------
		{
			name: "an empty object stays empty",
			in:   `{}`,
			want: `{}`,
		},
		{
			name: "a null property value is preserved",
			in:   `{"properties":{"a":null}}`,
			want: `{"properties":{"a":null}}`,
		},
		{
			name: "a null items is preserved",
			in:   `{"items":null}`,
			want: `{"items":null}`,
		},
		{
			name: "an empty anyOf is preserved",
			in:   `{"anyOf":[]}`,
			want: `{"anyOf":[]}`,
		},
		{
			name: "a top-level array is returned verbatim",
			in:   `[1,2]`,
			want: `[1,2]`,
		},
		{
			name: "a top-level string is returned verbatim",
			in:   `"x"`,
			want: `"x"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizeSchema(json.RawMessage(tt.in))
			if err != nil {
				t.Fatalf("SanitizeSchema(%s) = %v", tt.in, err)
			}
			if string(got) != tt.want {
				t.Errorf("SanitizeSchema(%s)\n got %s\nwant %s", tt.in, got, tt.want)
			}
		})
	}
}

// The realistic case: what Go schema inference emits for a tool with a pointer
// field, a slice field and a nested struct. This is the shape that actually
// reaches Gemini at setup.
func TestSanitizeSchemaInferredToolSchema(t *testing.T) {
	in := `{"type":"object","additionalProperties":false,"required":["path"],` +
		`"properties":{` +
		`"path":{"type":"string"},` +
		`"limit":{"type":["null","integer"]},` +
		`"tags":{"type":["null","array"],"items":{"type":"string"}},` +
		`"opts":{"type":"object","additionalProperties":false,"properties":{"deep":{"type":["null","boolean"]}}}` +
		`}}`
	want := `{"properties":{` +
		`"limit":{"nullable":true,"type":"integer"},` +
		`"opts":{"properties":{"deep":{"nullable":true,"type":"boolean"}},"type":"object"},` +
		`"path":{"type":"string"},` +
		`"tags":{"items":{"type":"string"},"nullable":true,"type":"array"}` +
		`},"required":["path"],"type":"object"}`

	got, err := SanitizeSchema(json.RawMessage(in))
	if err != nil {
		t.Fatalf("SanitizeSchema() = %v", err)
	}
	if string(got) != want {
		t.Errorf("SanitizeSchema()\n got %s\nwant %s", got, want)
	}
}

// sanitizeSchemaNode edits the map it is handed. SanitizeSchema only ever hands
// it a map decoded from fresh bytes, but the in-place contract is what makes
// that safe, so pin it directly.
func TestSanitizeSchemaNodeEditsInPlace(t *testing.T) {
	node := map[string]any{
		"additionalProperties": false,
		"type":                 []any{"null", "string"},
	}
	sanitizeSchemaNode(node)

	if _, ok := node["additionalProperties"]; ok {
		t.Error("additionalProperties survived")
	}
	if node["type"] != "string" {
		t.Errorf("type = %v, want \"string\"", node["type"])
	}
	if node["nullable"] != true {
		t.Errorf("nullable = %v, want true", node["nullable"])
	}
}
