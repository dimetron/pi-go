package acp

import (
	"errors"
	"testing"
	"time"
)

// TestStringifyRawValue covers every branch of the raw-value coercion used
// when pulling display strings out of untyped ACP tool-call payloads. The
// default branch returning "" rather than a Go-syntax dump is the point: an
// unexpected type must render as nothing, not as "map[foo:bar]" in the UI.
func TestStringifyRawValue(t *testing.T) {
	t.Parallel()

	cases := []struct {
		desc string
		in   any
		want string
	}{
		{"string is trimmed", "  hello  ", "hello"},
		{"string of only spaces collapses to empty", "   ", ""},
		{"empty string stays empty", "", ""},
		{"multiline string is trimmed at both ends", "\n\tgo test ./...\n", "go test ./..."},

		// time.Duration is a fmt.Stringer, so it takes the Stringer branch
		// rather than the numeric one despite having an integer kind.
		{"fmt.Stringer uses String()", 90 * time.Second, "1m30s"},

		{"float64 renders via %v", float64(2.5), "2.5"},
		{"float64 whole number drops the decimal", float64(7), "7"},
		{"int renders via %v", 42, "42"},
		{"int64 renders via %v", int64(-9), "-9"},
		{"bool true", true, "true"},
		{"bool false", false, "false"},

		// Everything else must be dropped, not stringified.
		{"nil yields empty", nil, ""},
		{"map yields empty rather than a Go dump", map[string]int{"a": 1}, ""},
		{"slice yields empty", []string{"a"}, ""},
		{"struct yields empty", struct{ A int }{1}, ""},
		{"uint is not in the numeric branch", uint(3), ""},
		{"float32 is not in the numeric branch", float32(1.5), ""},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			if got := stringifyRawValue(tc.in); got != tc.want {
				t.Fatalf("stringifyRawValue(%#v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestValidationError covers the string-backed error type. It carries its
// message as its own underlying value, so Error must round-trip it verbatim.
func TestValidationError(t *testing.T) {
	t.Parallel()

	t.Run("Error returns the message verbatim", func(t *testing.T) {
		t.Parallel()
		if got := ValidationError("missing session id").Error(); got != "missing session id" {
			t.Fatalf("Error() = %q, want %q", got, "missing session id")
		}
	})

	t.Run("empty message round-trips", func(t *testing.T) {
		t.Parallel()
		if got := ValidationError("").Error(); got != "" {
			t.Fatalf("Error() = %q, want empty", got)
		}
	})

	t.Run("validationError constructs a usable error", func(t *testing.T) {
		t.Parallel()
		err := validationError("bad input")
		if err == nil {
			t.Fatal("validationError returned nil")
		}
		if err.Error() != "bad input" {
			t.Fatalf("Error() = %q, want %q", err.Error(), "bad input")
		}
		var ve ValidationError
		if !errors.As(err, &ve) {
			t.Fatalf("error is not a ValidationError: %T", err)
		}
	})
}
