package tools

import "testing"

// BenchmarkLoadGitignorePatterns measures the tree walk that collects .gitignore
// files. grep (on fallback) and find (always) call this on every invocation.
func BenchmarkLoadGitignorePatterns(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		// A fresh Sandbox per iteration: the patterns are memoized per Sandbox,
		// so reusing one would measure the cache, not the walk.
		b.StopTimer()
		sb, err := NewSandbox("../..")
		if err != nil {
			b.Fatalf("NewSandbox: %v", err)
		}
		b.StartTimer()

		if _, err := sb.LoadGitignorePatterns(); err != nil {
			b.Fatal(err)
		}

		b.StopTimer()
		sb.Close()
		b.StartTimer()
	}
}

// BenchmarkLoadGitignoreCached measures the memoized path — repeated calls on a
// long-lived Sandbox, which is how grep/find actually use it within a session.
func BenchmarkLoadGitignoreCached(b *testing.B) {
	sb, err := NewSandbox("../..")
	if err != nil {
		b.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	b.ReportAllocs()
	for b.Loop() {
		if _, err := sb.LoadGitignorePatterns(); err != nil {
			b.Fatal(err)
		}
	}
}

// benchGrep runs grepHandler with ripgrep enabled, as it is in production.
// TestMain disables rg for the rest of the suite, so it must be re-enabled here
// or the benchmark measures the Go fallback in both cases.
func benchGrep(b *testing.B, pattern string) {
	b.Helper()
	if !rgAvailable {
		b.Skip("ripgrep not installed")
	}
	prev := grepRGDisabled
	grepRGDisabled = false
	b.Cleanup(func() { grepRGDisabled = prev })

	sb, err := NewSandbox("../..")
	if err != nil {
		b.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	in := GrepInput{Pattern: pattern}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := grepHandler(sb, in); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGrepNoMatch: the case that used to be pathological. rg exits 1 on
// no-match; treating that as a failure sent this down the Go fallback path.
// The pattern is built at runtime — a literal would match this file.
func BenchmarkGrepNoMatch(b *testing.B) { benchGrep(b, "zq9v"+"Z2mK"+"7pL4wR1t") }

// BenchmarkGrepWithMatch: the case that was always fast, as a control.
func BenchmarkGrepWithMatch(b *testing.B) { benchGrep(b, "func grepHandler") }
