package refs

import (
	"testing"
)

func TestParseRefs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []ParsedRef
	}{
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name:     "no refs",
			input:    "just a regular message",
			expected: nil,
		},
		{
			name:  "file ref basic",
			input: "@file:src/main.go",
			expected: []ParsedRef{
				{Type: RefFile, RawValue: "src/main.go", Value: "src/main.go"},
			},
		},
		{
			name:  "file ref with line range",
			input: "@file:src/main.go:10-25",
			expected: []ParsedRef{
				{
					Type:      RefFile,
					RawValue:  "src/main.go:10-25",
					Value:     "src/main.go",
					LineRange: &LineRange{Start: 10, End: 25},
				},
			},
		},
		{
			name:  "file ref with single line",
			input: "@file:src/main.go:42",
			expected: []ParsedRef{
				{
					Type:      RefFile,
					RawValue:  "src/main.go:42",
					Value:     "src/main.go",
					LineRange: &LineRange{Start: 42, End: 42},
				},
			},
		},
		{
			name:  "folder ref",
			input: "@folder:src/components",
			expected: []ParsedRef{
				{Type: RefFolder, RawValue: "src/components", Value: "src/components"},
			},
		},
		{
			name:  "diff ref",
			input: "@diff",
			expected: []ParsedRef{
				{Type: RefDiff, RawValue: "", Value: ""},
			},
		},
		{
			name:  "staged ref",
			input: "@staged",
			expected: []ParsedRef{
				{Type: RefStaged, RawValue: "", Value: ""},
			},
		},
		{
			name:  "git ref with number",
			input: "@git:5",
			expected: []ParsedRef{
				{Type: RefGit, RawValue: "5", Value: "5"},
			},
		},
		{
			name:  "url ref",
			input: "@url:https://example.com",
			expected: []ParsedRef{
				{Type: RefURL, RawValue: "https://example.com", Value: "https://example.com"},
			},
		},
		{
			name:  "multiple refs",
			input: "@file:src/main.go and @folder:src/components",
			expected: []ParsedRef{
				{Type: RefFile, RawValue: "src/main.go", Value: "src/main.go"},
				{Type: RefFolder, RawValue: "src/components", Value: "src/components"},
			},
		},
		{
			name:  "ref in sentence",
			input: "Check @file:README.md for details.",
			expected: []ParsedRef{
				{Type: RefFile, RawValue: "README.md", Value: "README.md"},
			},
		},
		{
			name:  "ref with trailing punctuation",
			input: "@file:src/main.go?",
			expected: []ParsedRef{
				{Type: RefFile, RawValue: "src/main.go", Value: "src/main.go"},
			},
		},
		{
			name:  "ref with comma",
			input: "@folder:src/utils, check it out",
			expected: []ParsedRef{
				{Type: RefFolder, RawValue: "src/utils", Value: "src/utils"},
			},
		},
		{
			name:  "mixed refs",
			input: "@diff @staged @git:3",
			expected: []ParsedRef{
				{Type: RefDiff, RawValue: "", Value: ""},
				{Type: RefStaged, RawValue: "", Value: ""},
				{Type: RefGit, RawValue: "3", Value: "3"},
			},
		},
		{
			name:     "unknown ref type skipped",
			input:    "@unknown:something",
			expected: nil,
		},
		{
			name:  "file with nested path",
			input: "@file:pkg/internal/foo/bar.go:1-10",
			expected: []ParsedRef{
				{
					Type:      RefFile,
					RawValue:  "pkg/internal/foo/bar.go:1-10",
					Value:     "pkg/internal/foo/bar.go",
					LineRange: &LineRange{Start: 1, End: 10},
				},
			},
		},
		{
			name:  "multiple refs with different types",
			input: "Look at @file:config.json and @url:https://api.example.com",
			expected: []ParsedRef{
				{Type: RefFile, RawValue: "config.json", Value: "config.json"},
				{Type: RefURL, RawValue: "https://api.example.com", Value: "https://api.example.com"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRefs(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("parseRefs(%q) returned %d refs, want %d", tt.input, len(got), len(tt.expected))
				return
			}
			for i, ref := range got {
				if ref.Type != tt.expected[i].Type {
					t.Errorf("parseRefs[%d].Type = %v, want %v", i, ref.Type, tt.expected[i].Type)
				}
				if ref.Value != tt.expected[i].Value {
					t.Errorf("parseRefs[%d].Value = %v, want %v", i, ref.Value, tt.expected[i].Value)
				}
				if ref.RawValue != tt.expected[i].RawValue {
					t.Errorf("parseRefs[%d].RawValue = %v, want %v", i, ref.RawValue, tt.expected[i].RawValue)
				}
				if tt.expected[i].LineRange == nil {
					if ref.LineRange != nil {
						t.Errorf("parseRefs[%d].LineRange = %v, want nil", i, ref.LineRange)
					}
				} else {
					if ref.LineRange == nil {
						t.Errorf("parseRefs[%d].LineRange = nil, want %v", i, tt.expected[i].LineRange)
					} else {
						if ref.LineRange.Start != tt.expected[i].LineRange.Start {
							t.Errorf("parseRefs[%d].LineRange.Start = %v, want %v", i, ref.LineRange.Start, tt.expected[i].LineRange.Start)
						}
						if ref.LineRange.End != tt.expected[i].LineRange.End {
							t.Errorf("parseRefs[%d].LineRange.End = %v, want %v", i, ref.LineRange.End, tt.expected[i].LineRange.End)
						}
					}
				}
			}
		})
	}
}

func TestStripTrailingPunctuation(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"hello.", "hello"},
		{"hello,", "hello"},
		{"hello!", "hello"},
		{"hello?", "hello"},
		{"hello;;", "hello"},
		{"hello...", "hello"},
		{"no trailing", "no trailing"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := stripTrailingPunctuation(tt.input)
			if got != tt.expected {
				t.Errorf("stripTrailingPunctuation(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestIsAlphanumericByte(t *testing.T) {
	tests := []struct {
		input    byte
		expected bool
	}{
		// Lowercase letters
		{'a', true},
		{'z', true},
		// Uppercase letters
		{'A', true},
		{'Z', true},
		// Digits
		{'0', true},
		{'9', true},
		// Non-alphanumeric
		{' ', false},
		{'.', false},
		{'/', false},
		{'_', false},
		{'-', false},
		{':', false},
		{'@', false},
		{0, false},
	}

	for _, tt := range tests {
		got := isAlphanumericByte(tt.input)
		if got != tt.expected {
			t.Errorf("isAlphanumericByte(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}
