package subagent

import (
	"testing"
	"time"
)

func TestResolveTimeout(t *testing.T) {
	t.Run("defaults when no overrides", func(t *testing.T) {
		tc := ResolveTimeout(0)
		if tc.Absolute != DefaultAbsoluteTimeout {
			t.Errorf("Absolute = %v, want %v", tc.Absolute, DefaultAbsoluteTimeout)
		}
		if tc.Inactivity != DefaultInactivityTimeout {
			t.Errorf("Inactivity = %v, want %v", tc.Inactivity, DefaultInactivityTimeout)
		}
	})

	t.Run("env var overrides default", func(t *testing.T) {
		t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "300000") // 5 minutes
		tc := ResolveTimeout(0)
		if tc.Absolute != 5*time.Minute {
			t.Errorf("Absolute = %v, want 5m", tc.Absolute)
		}
	})

	t.Run("agent frontmatter overrides env var", func(t *testing.T) {
		t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "300000") // 5 minutes
		tc := ResolveTimeout(60000)                   // 1 minute from frontmatter
		if tc.Absolute != time.Minute {
			t.Errorf("Absolute = %v, want 1m (frontmatter takes priority)", tc.Absolute)
		}
	})

	t.Run("inactivity capped at absolute", func(t *testing.T) {
		tc := ResolveTimeout(30000) // 30 seconds
		if tc.Inactivity > tc.Absolute {
			t.Errorf("Inactivity %v should not exceed Absolute %v", tc.Inactivity, tc.Absolute)
		}
		if tc.Inactivity != 30*time.Second {
			t.Errorf("Inactivity = %v, want 30s (capped to absolute)", tc.Inactivity)
		}
	})

	t.Run("invalid env var ignored", func(t *testing.T) {
		t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "not-a-number")
		tc := ResolveTimeout(0)
		if tc.Absolute != DefaultAbsoluteTimeout {
			t.Errorf("Absolute = %v, want default (invalid env ignored)", tc.Absolute)
		}
	})

	t.Run("negative env var ignored", func(t *testing.T) {
		t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "-1000")
		tc := ResolveTimeout(0)
		if tc.Absolute != DefaultAbsoluteTimeout {
			t.Errorf("Absolute = %v, want default (negative env ignored)", tc.Absolute)
		}
	})

	t.Run("zero env var ignored", func(t *testing.T) {
		t.Setenv("PI_SUBAGENT_TIMEOUT_MS", "0")
		tc := ResolveTimeout(0)
		if tc.Absolute != DefaultAbsoluteTimeout {
			t.Errorf("Absolute = %v, want default (zero env ignored)", tc.Absolute)
		}
	})
}

func TestInactivityTimer(t *testing.T) {
	t.Run("fires after timeout", func(t *testing.T) {
		timer := NewInactivityTimer(50 * time.Millisecond)
		defer timer.Stop()

		select {
		case <-timer.C():
			// Expected.
		case <-time.After(200 * time.Millisecond):
			t.Fatal("timer should have fired within 200ms")
		}
	})

	t.Run("reset extends deadline", func(t *testing.T) {
		timer := NewInactivityTimer(80 * time.Millisecond)
		defer timer.Stop()

		// Wait 50ms, then reset. Timer should not have fired yet.
		time.Sleep(50 * time.Millisecond)
		timer.Reset()

		// Wait another 50ms — should not fire (reset extended to 80ms from now).
		select {
		case <-timer.C():
			t.Fatal("timer fired too early after reset")
		case <-time.After(50 * time.Millisecond):
			// Expected: still waiting.
		}

		// Now wait for the remaining time + margin.
		select {
		case <-timer.C():
			// Expected.
		case <-time.After(100 * time.Millisecond):
			t.Fatal("timer should have fired after reset period elapsed")
		}
	})

	t.Run("stop prevents firing", func(t *testing.T) {
		timer := NewInactivityTimer(50 * time.Millisecond)
		timer.Stop()

		select {
		case <-timer.C():
			t.Fatal("stopped timer should not fire")
		case <-time.After(100 * time.Millisecond):
			// Expected.
		}
	})

	t.Run("multiple resets work", func(t *testing.T) {
		timer := NewInactivityTimer(60 * time.Millisecond)
		defer timer.Stop()

		for range 5 {
			time.Sleep(30 * time.Millisecond)
			timer.Reset()
		}

		// After 5 resets (150ms total), timer should still not have fired.
		select {
		case <-timer.C():
			t.Fatal("timer should not have fired during active resets")
		default:
			// Expected.
		}
	})
}
