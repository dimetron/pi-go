package tools

import (
	"strings"
	"testing"
	"time"
)

// A handoff has to explain itself. Without the limits the result says only that
// the command went quiet — not that the threshold it crossed was one the caller
// picked, which is the difference between waiting and fixing the call.
func TestSupervisor_HandoffReportsItsLimits(t *testing.T) {
	sup := fastSupervisor(t)

	out := run(t, sup, "sleep 30", 10*time.Second)

	if !out.Running {
		t.Fatalf("expected a backgrounded command, got %+v", out)
	}
	if want := (150 * time.Millisecond).String(); out.IdleTimeout != want {
		t.Errorf("IdleTimeout = %q, want %q", out.IdleTimeout, want)
	}
	if want := (10 * time.Second).String(); out.Timeout != want {
		t.Errorf("Timeout = %q, want %q", out.Timeout, want)
	}
	if out.Elapsed == "" {
		t.Error("Elapsed should be set on a handoff")
	}
	if !strings.Contains(out.Note, "idle_timeout") {
		t.Errorf("Note should name the limits, got %q", out.Note)
	}
}

// The limits survive the handoff: a poll several turns later can still say what
// the command was started under, without the caller having to remember.
func TestSupervisor_PollReportsTheSameLimits(t *testing.T) {
	sup := fastSupervisor(t)

	out := run(t, sup, "sleep 30", 10*time.Second)
	if out.Handle == "" {
		t.Fatal("expected a handle")
	}
	st, err := sup.readOutput(out.Handle, 0)
	if err != nil {
		t.Fatalf("readOutput: %v", err)
	}

	if st.IdleTimeout != out.IdleTimeout {
		t.Errorf("poll IdleTimeout = %q, want %q", st.IdleTimeout, out.IdleTimeout)
	}
	if st.Timeout != out.Timeout {
		t.Errorf("poll Timeout = %q, want %q", st.Timeout, out.Timeout)
	}
}

func TestLimitsHint(t *testing.T) {
	tests := []struct {
		name          string
		timeout, idle time.Duration
		want          []string
		notWant       []string
	}{
		{
			// The case that produced fifty-five polls of one handle: a
			// one-second idle limit backgrounds every build in this repo.
			name:    "a hair-trigger idle limit is called out",
			timeout: 2 * time.Minute,
			idle:    time.Second,
			want:    []string{"idle_timeout 1s", "timeout 2m0s", "rather than polling the handle"},
		},
		{
			name:    "an ordinary idle limit is stated, not argued with",
			timeout: 5 * time.Minute,
			idle:    90 * time.Second,
			want:    []string{"idle_timeout 1m30s", "timeout 5m0s"},
			notWant: []string{"rather than polling the handle"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := limitsHint(tt.timeout, tt.idle)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("limitsHint() = %q, want it to contain %q", got, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(got, w) {
					t.Errorf("limitsHint() = %q, should not contain %q", got, w)
				}
			}
		})
	}
}
