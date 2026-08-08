package subagent

import (
	"testing"
	"time"
)

// TestBundledAgentTimeoutsAreSane walks every shipped agent definition and
// resolves the timeout it would actually run with.
//
// This exists because `memory-compressor` shipped `timeout: 30`. The unit is
// milliseconds, so that agent was SIGKILLed 30ms after starting — every single
// time, before it could emit a token — and nothing anywhere reported the cause.
// The value reads perfectly reasonable if you assume seconds, which is exactly
// why a person reviewing the file did not catch it.
func TestBundledAgentTimeoutsAreSane(t *testing.T) {
	res, err := DiscoverAgents(t.TempDir(), ScopeBundled)
	if err != nil {
		t.Fatalf("DiscoverAgents: %v", err)
	}
	if len(res.All) == 0 {
		t.Fatal("no bundled agents were loaded")
	}

	for _, a := range res.All {
		t.Run(a.Name, func(t *testing.T) {
			cfg := ResolveTimeout(a.Timeout)

			// A second is not enough for a model round trip, so any resolved
			// absolute timeout below it means the agent can never succeed.
			if cfg.Absolute < time.Second {
				t.Errorf("agent %q resolves to a %v absolute timeout — it can never produce output "+
					"(frontmatter `timeout:` is in MILLISECONDS; got %d)", a.Name, cfg.Absolute, a.Timeout)
			}
			if cfg.Inactivity <= 0 {
				t.Errorf("agent %q resolves to a non-positive inactivity timeout %v", a.Name, cfg.Inactivity)
			}
		})
	}
}

func TestMemoryCompressorHasAWorkableTimeout(t *testing.T) {
	res, err := DiscoverAgents(t.TempDir(), ScopeBundled)
	if err != nil {
		t.Fatalf("DiscoverAgents: %v", err)
	}

	var found bool
	for _, a := range res.All {
		if a.Name != "memory-compressor" {
			continue
		}
		found = true
		cfg := ResolveTimeout(a.Timeout)
		if cfg.Absolute < time.Minute {
			t.Errorf("memory-compressor absolute timeout = %v, want at least a minute "+
				"(the value is in milliseconds)", cfg.Absolute)
		}
	}
	if !found {
		t.Fatal("memory-compressor is not among the bundled agents")
	}
}

// TestParseAgentRejectsSubSecondTimeout pins the guard that stops the
// milliseconds-read-as-seconds mistake from silently recurring.
func TestParseAgentRejectsSubSecondTimeout(t *testing.T) {
	const def = `---
name: too-eager
description: test
role: smol
timeout: 30
---
body
`
	cfg, err := parseAgentContent(def, "test.md")
	if err != nil {
		t.Fatalf("parseAgentContent: %v", err)
	}
	if cfg.Timeout != 0 {
		t.Errorf("Timeout = %d, want 0 (implausible value ignored so the default applies)", cfg.Timeout)
	}
	if got := ResolveTimeout(cfg.Timeout); got.Absolute != DefaultAbsoluteTimeout {
		t.Errorf("resolved absolute = %v, want the default %v", got.Absolute, DefaultAbsoluteTimeout)
	}
}

func TestParseAgentKeepsPlausibleTimeout(t *testing.T) {
	const def = `---
name: patient
description: test
role: smol
timeout: 600000
---
body
`
	cfg, err := parseAgentContent(def, "test.md")
	if err != nil {
		t.Fatalf("parseAgentContent: %v", err)
	}
	if cfg.Timeout != 600000 {
		t.Errorf("Timeout = %d, want 600000", cfg.Timeout)
	}
	if got := ResolveTimeout(cfg.Timeout); got.Absolute != 10*time.Minute {
		t.Errorf("resolved absolute = %v, want 10m", got.Absolute)
	}
}
