package main

import (
	"strings"
	"testing"
)

// govulncheck emits concatenated JSON objects, not an array, so the fixtures
// below are written the same way.
const (
	advisoryWithFix = `{"osv":{"id":"GO-2024-0001","affected":[{"package":{"name":"example.com/mod"},"ranges":[{"events":[{"introduced":"0"},{"fixed":"1.2.3"}]}]}]}}`
	advisoryNoFix   = `{"osv":{"id":"GO-2025-0002","affected":[{"package":{"name":"example.com/other"},"ranges":[{"events":[{"introduced":"0"}]}]}]}}`
	findingWithFix  = `{"finding":{"osv":"GO-2024-0001"}}`
	findingNoFix    = `{"finding":{"osv":"GO-2025-0002"}}`
)

func runGate(t *testing.T, input string) (int, string) {
	t.Helper()
	var out strings.Builder
	code, err := run(strings.NewReader(input), &out)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return code, out.String()
}

// A finding with no fix is the case that made a plain govulncheck run useless
// here: there is nothing to upgrade to, so failing on it would leave the check
// red forever and train everyone to ignore it.
func TestUnfixedFindingDoesNotFail(t *testing.T) {
	code, out := runGate(t, advisoryNoFix+findingNoFix)

	if code != 0 {
		t.Errorf("exit = %d, want 0 for a finding with no fix", code)
	}
	if !strings.Contains(out, "GO-2025-0002") {
		t.Error("the unfixed finding was not reported; it should still be visible")
	}
	if !strings.Contains(out, "no fix available") {
		t.Error("output does not say why the finding was not failed on")
	}
}

// The other half, and the reason the gate exists: a finding someone can act on
// must stop the build.
func TestFixedFindingFails(t *testing.T) {
	code, out := runGate(t, advisoryWithFix+findingWithFix)

	if code != 1 {
		t.Errorf("exit = %d, want 1 for a finding with a fix", code)
	}
	if !strings.Contains(out, "fixed in 1.2.3") {
		t.Errorf("output does not name the version to upgrade to:\n%s", out)
	}
}

// A fixable finding must fail even when unfixable ones are also present —
// otherwise the noise would hide the one actionable result.
func TestFixedFindingFailsAlongsideUnfixed(t *testing.T) {
	code, out := runGate(t, advisoryNoFix+advisoryWithFix+findingNoFix+findingWithFix)

	if code != 1 {
		t.Errorf("exit = %d, want 1 when any finding has a fix", code)
	}
	if !strings.Contains(out, "GO-2025-0002") || !strings.Contains(out, "GO-2024-0001") {
		t.Error("both findings should be reported")
	}
}

// An advisory govulncheck knows about but does not report a finding for does
// not concern this repository, and must not fail the build. govulncheck emits
// the whole advisory database, so this is the common case by volume.
func TestAdvisoryWithoutFindingIsIgnored(t *testing.T) {
	code, out := runGate(t, advisoryWithFix)

	if code != 0 {
		t.Errorf("exit = %d, want 0 when nothing was actually found", code)
	}
	if strings.Contains(out, "GO-2024-0001") {
		t.Error("an advisory with no finding should not be reported")
	}
}

// A finding whose advisory body never arrived is treated as actionable.
// Silence is the wrong default for a security gate: better to stop the build
// and have someone look than to pass because a field was missing.
func TestFindingWithoutAdvisoryFails(t *testing.T) {
	code, out := runGate(t, findingWithFix)

	if code != 1 {
		t.Errorf("exit = %d, want 1 for a finding with no advisory body", code)
	}
	if !strings.Contains(out, "unknown") {
		t.Errorf("output should mark the fix version as unknown:\n%s", out)
	}
}

func TestCleanRunPasses(t *testing.T) {
	code, out := runGate(t, "")

	if code != 0 {
		t.Errorf("exit = %d, want 0 for no findings", code)
	}
	if !strings.Contains(out, "No findings") {
		t.Errorf("unexpected output: %s", out)
	}
}
