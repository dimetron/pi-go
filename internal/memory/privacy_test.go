package memory

import (
	"testing"
)

func TestStripPrivate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single tag",
			input: "hello <private>secret</private> world",
			want:  "hello [PRIVATE] world",
		},
		{
			name:  "multiple tags",
			input: "<private>a</private> middle <private>b</private>",
			want:  "[PRIVATE] middle [PRIVATE]",
		},
		{
			name:  "nested content",
			input: "<private><div>nested <b>html</b></div></private>",
			want:  "[PRIVATE]",
		},
		{
			name:  "multiline",
			input: "before\n<private>\nline1\nline2\n</private>\nafter",
			want:  "before\n[PRIVATE]\nafter",
		},
		{
			name:  "no tags passthrough",
			input: "nothing private here",
			want:  "nothing private here",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "empty private tag",
			input: "a<private></private>b",
			want:  "a[PRIVATE]b",
		},
		{
			name:  "adjacent tags",
			input: "<private>x</private><private>y</private>",
			want:  "[PRIVATE][PRIVATE]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripPrivate(tt.input)
			if got != tt.want {
				t.Errorf("StripPrivate() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripPrivateFromMap(t *testing.T) {
	t.Run("nil map", func(t *testing.T) {
		got := StripPrivateFromMap(nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("flat map", func(t *testing.T) {
		m := map[string]any{
			"key":  "value <private>secret</private>",
			"safe": "no secrets",
			"num":  42,
		}
		got := StripPrivateFromMap(m)
		if got["key"] != "value [PRIVATE]" {
			t.Errorf("key = %q, want %q", got["key"], "value [PRIVATE]")
		}
		if got["safe"] != "no secrets" {
			t.Errorf("safe = %q, want %q", got["safe"], "no secrets")
		}
		if got["num"] != 42 {
			t.Errorf("num = %v, want 42", got["num"])
		}
	})

	t.Run("nested map", func(t *testing.T) {
		m := map[string]any{
			"outer": map[string]any{
				"inner": "has <private>deep secret</private>",
			},
		}
		got := StripPrivateFromMap(m)
		outer := got["outer"].(map[string]any)
		if outer["inner"] != "has [PRIVATE]" {
			t.Errorf("nested value = %q, want %q", outer["inner"], "has [PRIVATE]")
		}
	})

	t.Run("slice values", func(t *testing.T) {
		m := map[string]any{
			"list": []any{
				"clean",
				"has <private>token</private>",
				map[string]any{"nested": "<private>x</private>"},
			},
		}
		got := StripPrivateFromMap(m)
		list := got["list"].([]any)
		if list[0] != "clean" {
			t.Errorf("list[0] = %q, want %q", list[0], "clean")
		}
		if list[1] != "has [PRIVATE]" {
			t.Errorf("list[1] = %q, want %q", list[1], "has [PRIVATE]")
		}
		nested := list[2].(map[string]any)
		if nested["nested"] != "[PRIVATE]" {
			t.Errorf("nested = %q, want %q", nested["nested"], "[PRIVATE]")
		}
	})

	t.Run("does not mutate original", func(t *testing.T) {
		original := map[string]any{
			"key": "value <private>secret</private>",
		}
		_ = StripPrivateFromMap(original)
		if original["key"] != "value <private>secret</private>" {
			t.Error("original map was mutated")
		}
	})
}
