package cli

import (
	"slices"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/tools"
)

func TestUseCompactProfile(t *testing.T) {
	for _, tt := range []struct {
		provider, model string
		want            bool
	}{
		{"fm", "system", true},
		{"fm", "pcc", true},
		{"anthropic", "claude-sonnet-5", false},
		// An unknown window must not strip the tools.
		{"ollama", "no-such-model-anywhere", false},
	} {
		if got := useCompactProfile(tt.provider, tt.model); got != tt.want {
			t.Errorf("useCompactProfile(%q, %q) = %v, want %v", tt.provider, tt.model, got, tt.want)
		}
	}
}

// The compact start has to fit an 8K window with room left for the work. The
// tools are the real CoreTools declarations, measured the way the context gauge
// measures them, so the budget tracks what is actually sent.
func TestCompactProfile_FitsSmallWindow(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sb, err := tools.NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Close() })
	core, err := tools.CoreTools(sb)
	if err != nil {
		t.Fatal(err)
	}

	kept := essentialTools(core)
	var names []string
	for _, tl := range kept {
		names = append(names, tl.Name())
	}
	for _, want := range []string{"read", "write", "edit", "bash", "find", "ls"} {
		if !slices.Contains(names, want) {
			t.Errorf("essential tools %v are missing %q", names, want)
		}
	}
	// The search tool registers under exactly one of its two names.
	if slices.Contains(names, "ripgrep") == slices.Contains(names, "grep") {
		t.Errorf("want exactly one of ripgrep/grep in %v", names)
	}
	if len(kept) != 7 {
		t.Errorf("essential tools = %v, want 7", names)
	}
	if len(kept) >= len(core) {
		t.Errorf("essentialTools kept %d of %d: nothing was dropped", len(kept), len(core))
	}

	parts := compactInstructionParts()
	if parts.Rules != "" || parts.Skills != "" {
		t.Errorf("compact prompt carries rules (%d bytes) or skills (%d bytes)", len(parts.Rules), len(parts.Skills))
	}
	if !strings.Contains(parts.Base, agent.CompactSystemInstruction) {
		t.Errorf("compact prompt does not contain CompactSystemInstruction")
	}

	// A quarter of the 8,192-token window, leaving the rest for the transcript
	// and the reply. The default start measured 26,448 against the same window.
	const budgetTokens = 2048
	got := tokensFromChars(len(parts.String())) + tokensFromChars(declaredToolBytes(kept))
	if got > budgetTokens {
		t.Errorf("compact start is ~%d tokens, over the %d budget", got, budgetTokens)
	}
}

func TestCompactInstructionParts_SystemFlagWins(t *testing.T) {
	old := flagSystem
	t.Cleanup(func() { flagSystem = old })
	flagSystem = "custom prompt"

	if got := compactInstructionParts().String(); got != "custom prompt" {
		t.Errorf("compactInstructionParts() = %q, want the --system value alone", got)
	}
}
