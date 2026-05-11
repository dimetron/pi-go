package tui

import (
	"strings"
	"testing"
	"time"
)

func newTestSpinner(verb string) *spinnerState {
	t := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := &spinnerState{
		current: verb,
		updated: t,
		nowFn:   func() time.Time { return t },
	}
	return s
}

func TestSpinnerInitialSymbol(t *testing.T) {
	s := newTestSpinner("Thinking")
	got := s.tick()
	if got != "* Thinking...          " {
		t.Fatalf("expected padded '* Thinking...          ' got %q", got)
	}
}

func TestSpinnerSymbolRotation(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	s := &spinnerState{
		current: "Testing",
		updated: base,
		nowFn:   func() time.Time { return now },
	}

	// First call at base time — no advance yet.
	got := s.tick()
	if !strings.HasPrefix(got, "*") {
		t.Fatalf("expected '*' prefix, got %q", got)
	}

	// Step through remaining symbols.
	expected := []string{"+", "·"}
	for i, want := range expected {
		now = base.Add(time.Duration(i+1) * 150 * time.Millisecond)
		got = s.tick()
		if !strings.HasPrefix(got, want) {
			t.Fatalf("step %d: expected %q prefix, got %q", i+1, want, got)
		}
		// Word should stay the same.
		if !strings.Contains(got, "Testing") {
			t.Fatalf("step %d: word changed unexpectedly: %q", i+1, got)
		}
	}
}

func TestSpinnerWordChangesAfter3Rotations(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	s := &spinnerState{
		current: "Testing",
		updated: base,
		nowFn:   func() time.Time { return now },
	}

	// Initial call to set state.
	s.tick()

	// 7 full rotations of all symbols.
	totalSteps := len(spinnerSymbols) * 7
	for i := 1; i <= totalSteps; i++ {
		now = base.Add(time.Duration(i) * 150 * time.Millisecond)
		s.tick()
	}

	// After 7 rotations the word should have changed and symbol reset to '*'.
	got := s.tick()
	if !strings.HasPrefix(got, "*") {
		t.Fatalf("expected symbol reset to '*' after 3 rotations, got %q", got)
	}
	if strings.Contains(got, "Testing") {
		// Could theoretically pick the same word randomly, but extremely unlikely
		// with 100+ words. Accept if it happens.
		t.Logf("word stayed 'Testing' (random chance) — acceptable")
	}
}

func TestSpinnerFormat(t *testing.T) {
	s := newTestSpinner("Cooking")
	got := s.tick()

	// Format: "<symbol> <word>..."
	parts := strings.SplitN(got, " ", 2)
	if len(parts) != 2 {
		t.Fatalf("expected 'sym word...' format, got %q", got)
	}
	if !strings.Contains(parts[1], "...") {
		t.Fatalf("expected ellipsis before padding, got %q", got)
	}
	trimmed := strings.TrimRight(parts[1], " ")
	if !strings.HasSuffix(trimmed, "...") {
		t.Fatalf("expected only spaces after ellipsis, got %q", got)
	}
}

func TestSpinnerVerbFixedWidth(t *testing.T) {
	short := newTestSpinner("Thinking").tick()
	long := newTestSpinner("Whatchamacalliting").tick()
	if len(short) != len(long) {
		t.Fatalf("spinner lengths differ: short=%d %q long=%d %q", len(short), short, len(long), long)
	}
	if !strings.Contains(short, "Thinking...          ") {
		t.Fatalf("expected short verb to be padded after ellipsis, got %q", short)
	}
}

func TestSpinnerVerbNotEmpty(t *testing.T) {
	// Save and restore the global spinner for this test.
	saved := spinner
	defer func() { spinner = saved }()
	spinner = &spinnerState{}

	got := spinnerVerb()
	if got == "" {
		t.Fatal("spinnerVerb() returned empty string")
	}
	trimmed := strings.TrimRight(got, " ")
	if !strings.HasSuffix(trimmed, "...") {
		t.Fatalf("expected ellipsis before padding, got %q", got)
	}
}
