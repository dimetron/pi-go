package piagent

import (
	"os"
	"strings"
	"testing"
)

// TestLogPathPointsAtARealFile is the assertion behind WithCompactNotify's
// rationale: piagent passes no notifier on the grounds that "an embedder reads
// the outcome in the session log". That is only true if the log is reachable,
// which needs this accessor — internal/logger is under internal/ and cannot be
// called from outside the module.
func TestLogPathPointsAtARealFile(t *testing.T) {
	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "ok"})

	got := ag.LogPath()
	if got == "" {
		t.Fatal("LogPath() is empty; an embedder told to read the session log has no way to find it")
	}
	if !strings.HasSuffix(got, ".log") {
		t.Errorf("LogPath() = %q, want a .log file", got)
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("LogPath() = %q, but that file does not exist: %v", got, err)
	}
}

// TestLogPathIsNilSafe pins the documented "" contract rather than a panic.
// A failed log file costs logging and nothing else, which is the invariant the
// Agent's nil-safe sessionLog field exists to protect.
func TestLogPathIsNilSafe(t *testing.T) {
	ag := &Agent{}
	if got := ag.LogPath(); got != "" {
		t.Errorf("LogPath() on an agent with no log = %q, want \"\"", got)
	}
}

// TestResolveSummarizerHonoursTheOption is the point of WithSummarizer: the
// summarizer is a separate model from the one holding the conversation.
func TestResolveSummarizerHonoursTheOption(t *testing.T) {
	session := &fakeLLM{name: "session-model", reply: "ok"}
	summary := &fakeLLM{name: "summarizer-model", reply: "summary"}

	o := defaultOptions()
	WithSummarizer(summary)(&o)

	if got := resolveSummarizer(o, session).Name(); got != "summarizer-model" {
		t.Errorf("resolveSummarizer = %q, want summarizer-model — the option was ignored", got)
	}
}

// TestResolveSummarizerFallsBackToSessionModel is the control: without the
// option nothing changes, which is what makes the override meaningful.
func TestResolveSummarizerFallsBackToSessionModel(t *testing.T) {
	session := &fakeLLM{name: "session-model", reply: "ok"}

	if got := resolveSummarizer(defaultOptions(), session).Name(); got != "session-model" {
		t.Errorf("resolveSummarizer = %q, want the session model when no summarizer is set", got)
	}
}

// TestWithCompactNotifyReachesTheHook checks the notifier is threaded rather
// than merely stored: an embedder without one must still be able to read the
// outcome, which is the fallback the option's doc promises.
func TestWithCompactNotifyReachesTheHook(t *testing.T) {
	ag := newTestAgent(t, &fakeLLM{name: "fake", reply: "ok"},
		WithCompactNotify(func(string) {}),
	)
	if ag.LogPath() == "" {
		t.Error("no log path, so an embedder without a notifier cannot read the outcome")
	}
}

// TestCompactNotifyAbsentByDefault keeps silence the default: an embedder that
// asks for nothing must not start receiving output on a stream it never chose.
func TestCompactNotifyAbsentByDefault(t *testing.T) {
	if defaultOptions().compactNotify != nil {
		t.Error("a notifier is installed without WithCompactNotify")
	}
}

// TestCompactNotifyIsCallable checks the option stores the exact func it was
// given, so the text an embedder receives is the hook's, not a reworded copy.
func TestCompactNotifyIsCallable(t *testing.T) {
	var got []string
	o := defaultOptions()
	WithCompactNotify(func(s string) { got = append(got, s) })(&o)

	if o.compactNotify == nil {
		t.Fatal("WithCompactNotify did not set options.compactNotify")
	}
	o.compactNotify("compacted 1234 tokens")
	if len(got) != 1 || got[0] != "compacted 1234 tokens" {
		t.Errorf("notifier received %v, want the one line it was called with", got)
	}
}
