package tools

import "testing"

// noMatchPattern is assembled at runtime on purpose. Written as a literal, the
// pattern would appear in this source file, and a grep of the repo would match
// the test itself — silently turning the no-match case into a match.
func noMatchPattern() string {
	return "zq9v" + "Z2mK" + "7pL4wR1t"
}

// withRG enables ripgrep for one test. TestMain sets grepRGDisabled = true for
// the whole suite, which is what let the exit-code bug go unnoticed: every test
// took the Go fallback, so the ripgrep path was never exercised.
func withRG(t *testing.T) {
	t.Helper()
	if !rgAvailable {
		t.Skip("ripgrep not installed")
	}
	prev := grepRGDisabled
	grepRGDisabled = false
	t.Cleanup(func() { grepRGDisabled = prev })
}

// ripgrep exits 1 when it finds no matches. That is a valid empty result, not a
// failure, and must not be reported as an error — doing so sent grepHandler down
// the Go fallback path, which re-derived the same empty answer with a full tree
// walk (10x slower, ~3000x the allocations).
func TestGrepWithRG_NoMatchIsNotAnError(t *testing.T) {
	withRG(t)

	sb, err := NewSandbox("../..")
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	out, err := grepWithRG(sb, GrepInput{Pattern: noMatchPattern()}, ".")
	if err != nil {
		t.Fatalf("no-match must not be an error, got: %v", err)
	}
	if len(out.Matches) != 0 || out.TotalMatches != 0 {
		t.Fatalf("expected zero matches, got %d (total %d)", len(out.Matches), out.TotalMatches)
	}
}

// A real ripgrep failure (bad regex → exit 2) must still surface as an error so
// grepHandler can fall back to the Go implementation.
func TestGrepWithRG_RealFailureStillErrors(t *testing.T) {
	withRG(t)

	sb, err := NewSandbox("../..")
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	if _, err := grepWithRG(sb, GrepInput{Pattern: "a(b"}, "."); err == nil {
		t.Fatal("expected an error for an invalid regex (rg exits 2), got nil")
	}
}

// The matching path must keep working.
func TestGrepWithRG_MatchStillWorks(t *testing.T) {
	withRG(t)

	sb, err := NewSandbox("../..")
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	out, err := grepWithRG(sb, GrepInput{Pattern: "func grepHandler"}, ".")
	if err != nil {
		t.Fatalf("grepWithRG: %v", err)
	}
	if len(out.Matches) == 0 {
		t.Fatal("expected at least one match for \"func grepHandler\"")
	}
}

// End to end through grepHandler with ripgrep live: a miss returns cleanly and
// must not fall back.
func TestGrepHandler_NoMatchWithRG(t *testing.T) {
	withRG(t)

	sb, err := NewSandbox("../..")
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	out, err := grepHandler(sb, GrepInput{Pattern: noMatchPattern()})
	if err != nil {
		t.Fatalf("grepHandler: %v", err)
	}
	if len(out.Matches) != 0 {
		t.Fatalf("expected zero matches, got %d", len(out.Matches))
	}
}
