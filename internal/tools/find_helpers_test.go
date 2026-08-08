package tools

import "testing"

func TestMatchDoublestar(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"src/**/*.go", "src/pkg/main.go", true},
		{"src/**/*.go", "other/pkg/main.go", false},
		{"src/**/*.go", "src/main.go", true},
		{"**/*.go", "pkg/main.go", true},
		{"*.go", "pkg/main.go", false},
		{"src/**/", "src/pkg/main.go", true},
		{"src/**/", "other/main.go", false},
	}
	for _, tt := range tests {
		if got := matchDoublestar(tt.pattern, tt.path); got != tt.want {
			t.Errorf("matchDoublestar(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
		}
	}
}

func TestNormalizeGlobPattern(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"**/*.go", "*.go"},
		{"**/**/*.go", "*.go"},
		{"src/*.go", "src/*.go"},
	} {
		if got := normalizeGlobPattern(tt.input); got != tt.want {
			t.Errorf("normalizeGlobPattern(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
