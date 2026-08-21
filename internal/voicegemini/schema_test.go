package voicegemini

import (
	"encoding/json"
	"testing"
)

func TestSanitizeSchema(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// Emitted for every struct by Go schema inference; the field does
			// not exist in Gemini's Schema at all and closes the session at
			// setup with a 1007.
			name: "drops additionalProperties at the root",
			in:   `{"type":"object","additionalProperties":false}`,
			want: `{"type":"object"}`,
		},
		{
			name: "drops additionalProperties at every depth",
			in:   `{"type":"object","properties":{"a":{"type":"object","additionalProperties":false}}}`,
			want: `{"properties":{"a":{"type":"object"}},"type":"object"}`,
		},
		{
			// Emitted for every pointer or slice field; Gemini's type is a
			// single enum, not a list.
			name: "collapses a nullable type union",
			in:   `{"type":["null","array"]}`,
			want: `{"nullable":true,"type":"array"}`,
		},
		{
			name: "collapses a type union without null",
			in:   `{"type":["string"]}`,
			want: `{"type":"string"}`,
		},
		{
			name: "a null-only type union drops the type",
			in:   `{"type":["null"]}`,
			want: `{"nullable":true}`,
		},
		{
			name: "recurses into items",
			in:   `{"type":"array","items":{"type":["null","string"]}}`,
			want: `{"items":{"nullable":true,"type":"string"},"type":"array"}`,
		},
		{
			name: "recurses into anyOf",
			in:   `{"anyOf":[{"type":"object","additionalProperties":false}]}`,
			want: `{"anyOf":[{"type":"object"}]}`,
		},
		{
			// Total and permissive by contract: an unknown keyword passes
			// through rather than failing enablement.
			name: "passes unknown keywords through",
			in:   `{"type":"string","description":"x","pattern":"^a"}`,
			want: `{"description":"x","pattern":"^a","type":"string"}`,
		},
		{
			name: "a non-object schema is returned verbatim",
			in:   `null`,
			want: `null`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizeSchema(json.RawMessage(tt.in))
			if err != nil {
				t.Fatalf("SanitizeSchema() = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("SanitizeSchema(%s)\n got %s\nwant %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeSchemaRejectsInvalidJSON(t *testing.T) {
	if _, err := SanitizeSchema(json.RawMessage(`{not json`)); err == nil {
		t.Fatal("SanitizeSchema() = nil error, want a decode failure")
	}
}

// The function must never mutate a schema shared with pi's other tool paths.
func TestSanitizeSchemaDoesNotMutateInput(t *testing.T) {
	in := json.RawMessage(`{"type":"object","additionalProperties":false}`)
	before := string(in)
	if _, err := SanitizeSchema(in); err != nil {
		t.Fatalf("SanitizeSchema() = %v", err)
	}
	if string(in) != before {
		t.Errorf("input was mutated: %s", in)
	}
}
