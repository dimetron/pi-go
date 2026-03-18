package tools

import (
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestCollectCoerceProps(t *testing.T) {
	t.Run("nil schema", func(t *testing.T) {
		intP, boolP := collectCoerceProps(nil)
		if len(intP) != 0 || len(boolP) != 0 {
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
		intP, boolP := collectCoerceProps(schema)
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
		intP, boolP := collectCoerceProps(schema)
		if len(intP) != 0 || len(boolP) != 0 {
			t.Error("expected empty maps for schema with no properties")
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
}
