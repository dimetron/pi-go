package lsp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHoverResultUnmarshal covers every shape the LSP spec allows for
// Hover.contents. Decoding straight into MarkupContent — which this client
// used to do — fails on the array and string forms; jdtls returns an array,
// so hover was broken against it before the custom unmarshaller.
func TestHoverResultUnmarshal(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantKind string
		wantSubs []string
	}{
		{
			name:     "markup content object",
			raw:      `{"contents":{"kind":"markdown","value":"**String** greeting"}}`,
			wantKind: "markdown",
			wantSubs: []string{"String", "greeting"},
		},
		{
			name:     "plain string",
			raw:      `{"contents":"String greeting"}`,
			wantKind: "plaintext",
			wantSubs: []string{"String"},
		},
		{
			name:     "marked string object",
			raw:      `{"contents":{"language":"java","value":"String greeting"}}`,
			wantKind: "plaintext",
			wantSubs: []string{"String"},
		},
		{
			// The jdtls shape that used to fail with
			// "cannot unmarshal array into ... MarkupContent".
			name:     "array of marked strings",
			raw:      `{"contents":[{"language":"java","value":"String greeting"},{"language":"java","value":"com.example.Hello.main(String[])"}]}`,
			wantKind: "plaintext",
			wantSubs: []string{"String greeting", "Hello.main"},
		},
		{
			name:     "array of mixed shapes",
			raw:      `{"contents":["first",{"kind":"markdown","value":"second"}]}`,
			wantKind: "markdown",
			wantSubs: []string{"first", "second"},
		},
		{
			name:     "empty array",
			raw:      `{"contents":[]}`,
			wantKind: "",
			wantSubs: nil,
		},
		{
			name:     "null contents",
			raw:      `{"contents":null}`,
			wantKind: "",
			wantSubs: nil,
		},
		{
			// Not a shape the spec defines, but a server can still send it;
			// it must not abort the request.
			name:     "unsupported number",
			raw:      `{"contents":5}`,
			wantKind: "",
			wantSubs: nil,
		},
		{
			name:     "array containing an unsupported shape",
			raw:      `{"contents":["kept",7,"also kept"]}`,
			wantKind: "plaintext",
			wantSubs: []string{"kept", "also kept"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h HoverResult
			if err := json.Unmarshal([]byte(tt.raw), &h); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if h.Contents.Kind != tt.wantKind {
				t.Errorf("kind = %q, want %q", h.Contents.Kind, tt.wantKind)
			}
			for _, want := range tt.wantSubs {
				if !strings.Contains(h.Contents.Value, want) {
					t.Errorf("value %q missing %q", h.Contents.Value, want)
				}
			}
		})
	}
}

// TestHoverResultUnmarshal_Range pins that the range field still decodes
// alongside the custom contents handling.
func TestHoverResultUnmarshal_Range(t *testing.T) {
	raw := `{"contents":"x","range":{"start":{"line":5,"character":27},"end":{"line":5,"character":35}}}`
	var h HoverResult
	if err := json.Unmarshal([]byte(raw), &h); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if h.Range == nil {
		t.Fatal("range is nil")
	}
	if h.Range.Start.Line != 5 || h.Range.Start.Character != 27 {
		t.Errorf("range start = %+v, want line 5 char 27", h.Range.Start)
	}
}

// TestHoverResultUnmarshal_Invalid ensures malformed input surfaces an error
// rather than being silently swallowed.
func TestHoverResultUnmarshal_Invalid(t *testing.T) {
	var h HoverResult
	if err := json.Unmarshal([]byte(`{"contents":`), &h); err == nil {
		t.Error("expected an error for malformed JSON")
	}
}
