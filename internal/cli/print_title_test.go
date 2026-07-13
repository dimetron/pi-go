package cli

import (
	"strings"
	"testing"
)

func TestDerivePrintTitle(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello world", "hello world"},
		{"first line\nsecond line", "first line"},
		{"   ", ""},
		{"  trimmed  ", "trimmed"},
		{"a\nb\nc", "a"},
		{"", ""},
		// Long prompts are truncated with an ellipsis to keep meta.json small.
		{strings.Repeat("a", 250), strings.Repeat("a", 199) + "…"},
	}
	for _, c := range cases {
		got := derivePrintTitle(c.in)
		if got != c.want {
			t.Errorf("derivePrintTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
