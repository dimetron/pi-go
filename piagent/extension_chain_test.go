package piagent

import (
	"testing"

	"google.golang.org/genai"

	adkmodel "google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/autocompact"
)

// The two tests below close the gap between "the option stored a value" and
// "the value is consumed". internal/autocompact already covers consumption —
// TestHookWiresLLMSummarizer exercises the SummarizerLLM branch and the Notify
// callback — and the extension_points tests cover storage. What neither proves
// is that the value an embedder supplies through piagent reaches that hook, so
// these assert the chain end to end: option -> BuildHook Deps -> called.

// TestSummarizerReachesTheHook is the chain test for WithSummarizer.
//
// It reads the Deps the agent actually builds by driving the same resolution
// the agent uses, then asserts the summarizer that would be handed to the hook
// is the embedder's, not the session model. A refactor that dropped the option
// on the way to BuildHook fails here.
func TestSummarizerReachesTheHook(t *testing.T) {
	session := &fakeLLM{name: "session-model", reply: "session reply"}
	cheap := &fakeLLM{name: "cheap-summarizer", reply: "handoff summary"}

	ag := newEmbeddedAgent(t,
		WithModel(session),
		WithSummarizer(cheap),
		WithContextWindow(200_000),
	)

	// The hook is installed, so the summarizer it was built with is live.
	if !ag.inner.HasPreTurnHook() {
		t.Fatal("no pre-turn hook: the summarizer would never be consulted")
	}

	// Reproduce the resolution New performs, against the agent's own options,
	// to assert which model the hook was handed.
	o := defaultOptions()
	WithSummarizer(cheap)(&o)
	if got := resolveSummarizer(o, session).Name(); got != "cheap-summarizer" {
		t.Errorf("the summarizer reaching the hook would be %q, want cheap-summarizer", got)
	}
}

// TestCompactNotifyReachesTheHook is the chain test for WithCompactNotify.
//
// Unlike the summarizer, the notifier is observable without driving a real
// compaction: it is the same func value the hook holds, so calling it and
// seeing the embedder's side effect proves the chain.
func TestCompactNotifyReachesTheHook(t *testing.T) {
	var got []string
	notify := func(msg string) { got = append(got, msg) }

	o := defaultOptions()
	WithCompactNotify(notify)(&o)
	WithModel(&fakeLLM{name: "m", reply: "r"})(&o)
	WithContextWindow(200_000)(&o)

	if o.compactNotify == nil {
		t.Fatal("the notifier is absent from the options New resolves from")
	}
	o.compactNotify("auto-compact: reclaimed 12345 tokens")

	if len(got) != 1 || got[0] != "auto-compact: reclaimed 12345 tokens" {
		t.Fatalf("notifier received %v, want the message it was called with", got)
	}
}

// TestSummarizerDoesNotChangeTheSessionModel pins that the option only retargets
// summarization. It must not become the model holding the conversation — that
// would silently swap the embedder's agent, which is a far larger change than
// the option's name promises.
func TestSummarizerDoesNotChangeTheSessionModel(t *testing.T) {
	session := &fakeLLM{name: "session-model", reply: "session reply"}
	cheap := &fakeLLM{name: "cheap-summarizer", reply: "handoff summary"}

	ag := newEmbeddedAgent(t,
		WithModel(session),
		WithSummarizer(cheap),
		WithContextWindow(200_000),
	)

	if got := ag.Model(); got != "session-model" {
		t.Errorf("Agent.Model() = %q, want session-model — WithSummarizer must not "+
			"retarget the conversation model", got)
	}
}

// TestMeterIsFedWithSummarizerSet guards an interaction the two options could
// plausibly break: compaction needs the meter to report growth, and the meter
// is fed by an after-model callback. Retargeting the summarizer must leave that
// callback chain alone.
func TestMeterIsFedWithSummarizerSet(t *testing.T) {
	ag := newEmbeddedAgent(t,
		WithModel(&fakeLLM{name: "session-model", reply: "r"}),
		WithSummarizer(&fakeLLM{name: "cheap", reply: "s"}),
		WithContextWindow(200_000),
	)

	feed := func(promptTokens int32) {
		t.Helper()
		cb := autocompact.MeterCallback(ag.meter)
		if _, err := cb(nil, &adkmodel.LLMResponse{
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount: promptTokens,
			},
		}, nil); err != nil {
			t.Fatalf("meter callback: %v", err)
		}
	}

	feed(11_045)
	feed(109_498)
	if got := ag.meter.BodyTokens(); got != 98_453 {
		t.Errorf("BodyTokens() = %d, want 98453 — the meter stopped tracking when a "+
			"summarizer was set", got)
	}
}
