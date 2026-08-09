package subagent

import (
	"os"
	"strconv"
	"time"
)

// Default timeout values for subagent processes.
const (
	// DefaultAbsoluteTimeout is the maximum wall-clock time a subagent can run.
	//
	// This is a backstop against a pathological agent, not the working limit —
	// the inactivity timeout below is what actually distinguishes a stuck agent
	// from a slow one, so this can afford to be generous. It was briefly 5
	// minutes, which killed productive agents mid-answer: a review across a
	// day's commits legitimately runs longer than that, and cutting it off
	// discards everything it had produced.
	DefaultAbsoluteTimeout = 10 * time.Minute

	// DefaultInactivityTimeout is how long a subagent can go without producing output.
	//
	// Silence is the signal that matters. An agent that is streaming tokens or
	// reporting tool calls is working, however long it takes; one that has said
	// nothing for five minutes is wedged. It was briefly 120s, which killed
	// productive worktree agents mid-task: a long `bash` command (a build, a
	// test run, a git operation) that produces no output for two minutes is
	// working, not stuck, and cutting it off discarded everything it had done.
	DefaultInactivityTimeout = 5 * time.Minute
)

// TimeoutConfig holds resolved timeout values for a subagent.
type TimeoutConfig struct {
	Absolute   time.Duration // Maximum wall-clock time.
	Inactivity time.Duration // Maximum time without output.
}

// ResolveTimeout determines the timeout configuration for a subagent.
// Priority: agent frontmatter > PI_SUBAGENT_TIMEOUT_MS env var > default.
// agentTimeoutMs is the timeout field from AgentConfig (0 means unset).
func ResolveTimeout(agentTimeoutMs int) TimeoutConfig {
	absolute := DefaultAbsoluteTimeout
	inactivity := DefaultInactivityTimeout

	// Check environment variable overrides.
	if envMs := os.Getenv("PI_SUBAGENT_TIMEOUT_MS"); envMs != "" {
		if ms, err := strconv.Atoi(envMs); err == nil && ms > 0 {
			absolute = time.Duration(ms) * time.Millisecond
		}
	}
	// The inactivity limit is the one that decides whether a long-running agent
	// is working or wedged, so it needs its own knob rather than being reachable
	// only through the absolute cap.
	if envMs := os.Getenv("PI_SUBAGENT_INACTIVITY_MS"); envMs != "" {
		if ms, err := strconv.Atoi(envMs); err == nil && ms > 0 {
			inactivity = time.Duration(ms) * time.Millisecond
		}
	}

	// Agent frontmatter takes highest priority (overrides absolute timeout).
	if agentTimeoutMs > 0 {
		absolute = time.Duration(agentTimeoutMs) * time.Millisecond
	}

	// Inactivity timeout should never exceed absolute timeout.
	if inactivity > absolute {
		inactivity = absolute
	}

	return TimeoutConfig{
		Absolute:   absolute,
		Inactivity: inactivity,
	}
}

// InactivityTimer tracks output activity and signals when the inactivity timeout expires.
type InactivityTimer struct {
	timer   *time.Timer
	timeout time.Duration
}

// NewInactivityTimer creates a timer that fires after the given duration of inactivity.
func NewInactivityTimer(timeout time.Duration) *InactivityTimer {
	return &InactivityTimer{
		timer:   time.NewTimer(timeout),
		timeout: timeout,
	}
}

// Reset restarts the inactivity countdown. Call this when output is received.
func (t *InactivityTimer) Reset() {
	if !t.timer.Stop() {
		// Drain channel if timer already fired.
		select {
		case <-t.timer.C:
		default:
		}
	}
	t.timer.Reset(t.timeout)
}

// C returns the channel that fires when the inactivity timeout expires.
func (t *InactivityTimer) C() <-chan time.Time {
	return t.timer.C
}

// Stop stops the timer. Call when the process completes normally.
func (t *InactivityTimer) Stop() {
	t.timer.Stop()
}
