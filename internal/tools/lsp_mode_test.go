package tools

import (
	"slices"
	"testing"

	"github.com/dimetron/pi-go/internal/lsp"
)

func TestParseLSPMode(t *testing.T) {
	tests := []struct {
		in     string
		want   LSPMode
		wantOK bool
	}{
		{"", LSPMin, true},
		{"min", LSPMin, true},
		{"minimal", LSPMin, true},
		{"default", LSPMin, true},
		{"full", LSPFull, true},
		{"all", LSPFull, true},
		{"true", LSPFull, true},
		{"1", LSPFull, true},
		{"  FULL  ", LSPFull, true},
		{"off", LSPOff, true},
		{"false", LSPOff, true},
		{"0", LSPOff, true},
		{"none", LSPOff, true},
		{"banana", LSPMin, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, ok := ParseLSPMode(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("ParseLSPMode(%q) = (%q, %v), want (%q, %v)", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func toolNames(ts []interface{ Name() string }) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.Name())
	}
	return out
}

func TestLSPToolsForMode(t *testing.T) {
	mgr := lsp.NewManager(nil)

	tests := []struct {
		mode LSPMode
		want []string
	}{
		{LSPOff, nil},
		{LSPMin, []string{"lsp-symbols", "lsp-diagnostics"}},
		{LSPFull, []string{
			"lsp-diagnostics", "lsp-definition", "lsp-references", "lsp-hover",
			"lsp-symbols", "lsp-workspace-symbol", "lsp-code-action",
		}},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			ts, err := LSPToolsFor(mgr, tt.mode)
			if err != nil {
				t.Fatalf("LSPToolsFor(%q): %v", tt.mode, err)
			}
			named := make([]interface{ Name() string }, len(ts))
			for i, x := range ts {
				named[i] = x
			}
			got := toolNames(named)
			if len(tt.want) == 0 {
				if len(got) != 0 {
					t.Fatalf("mode %q returned %v, want none", tt.mode, got)
				}
				return
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("mode %q = %v, want %v", tt.mode, got, tt.want)
			}
		})
	}
}

// TestLSPToolsDefaultsToMinimal pins the default: the exported LSPTools is what
// every session pays for, so it must be the two-tool set, not all seven.
func TestLSPToolsDefaultsToMinimal(t *testing.T) {
	ts, err := LSPTools(lsp.NewManager(nil))
	if err != nil {
		t.Fatalf("LSPTools: %v", err)
	}
	if len(ts) != 2 {
		named := make([]interface{ Name() string }, len(ts))
		for i, x := range ts {
			named[i] = x
		}
		t.Fatalf("LSPTools returned %d tools (%v), want the 2 minimal ones", len(ts), toolNames(named))
	}
}

// TestMinimalIsSubsetOfFull keeps the two lists honest: whatever min advertises
// must still exist in full, so raising the mode never removes a tool.
func TestMinimalIsSubsetOfFull(t *testing.T) {
	mgr := lsp.NewManager(nil)
	minTools, _ := LSPToolsFor(mgr, LSPMin)
	fullTools, _ := LSPToolsFor(mgr, LSPFull)

	fullNames := make([]string, 0, len(fullTools))
	for _, x := range fullTools {
		fullNames = append(fullNames, x.Name())
	}
	for _, x := range minTools {
		if !slices.Contains(fullNames, x.Name()) {
			t.Fatalf("minimal tool %q is missing from the full set %v", x.Name(), fullNames)
		}
	}
}
