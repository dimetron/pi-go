package provider

import "testing"

func TestResolve(t *testing.T) {
	tests := []struct {
		model    string
		wantProv string
		wantErr  bool
	}{
		{"claude-sonnet-4-20250514", "anthropic", false},
		{"claude-opus-4-20250514", "anthropic", false},
		{"gpt-4o", "openai", false},
		{"o3-mini", "openai", false},
		{"gemini-2.5-pro", "gemini", false},
		{"", "", true},
		{"llama-3", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			info, err := Resolve(tt.model)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for model %q", tt.model)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Provider != tt.wantProv {
				t.Errorf("got provider %q, want %q", info.Provider, tt.wantProv)
			}
			if info.Model != tt.model {
				t.Errorf("got model %q, want %q", info.Model, tt.model)
			}
		})
	}
}
