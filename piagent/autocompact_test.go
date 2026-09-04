package piagent

import (
	"context"
	"testing"

	"google.golang.org/genai"

	adkmodel "google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/autocompact"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/testenv"
)

// newEmbeddedAgent builds an agent the way an embedder would, rooted in temp
// directories so the test never touches the developer's real ~/.pi-go.
func newEmbeddedAgent(t *testing.T, opts ...Option) *Agent {
	t.Helper()
	testenv.SetHome(t, t.TempDir())

	base := []Option{
		WithModel(&fakeLLM{name: "test-model"}),
		WithWorkingDir(t.TempDir()),
		WithSessionDir(t.TempDir()),
	}
	ag, err := New(context.Background(), append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = ag.Close() })
	return ag
}

// TestAutoCompactWiredForEmbedders covers the wiring an embedder gets. Before
// this, piagent was the one entry point with no compaction at all: an embedded
// agent re-sends its whole transcript every turn exactly as the CLI does, and
// nothing reclaimed it.
//
// The hook is installed either way. What the stated window decides is whether
// it can do anything: with no window there is no denominator, and the hook
// reports that once instead of silently no-opping forever.
func TestAutoCompactWiredForEmbedders(t *testing.T) {
	tests := []struct {
		desc       string
		opts       []Option
		wantWindow int64
	}{
		{
			desc:       "no window stated leaves compaction inert",
			wantWindow: 0,
		},
		{
			desc:       "WithContextWindow states it",
			opts:       []Option{WithContextWindow(200_000)},
			wantWindow: 200_000,
		},
		{
			desc:       "a non-positive window is not a window",
			opts:       []Option{WithContextWindow(0)},
			wantWindow: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			ag := newEmbeddedAgent(t, tt.opts...)
			if !ag.inner.HasPreTurnHook() {
				t.Error("HasPreTurnHook() = false; every embedded agent gets the hook")
			}
			if got := ag.meter.ContextWindowSize(); got != tt.wantWindow {
				t.Errorf("meter.ContextWindowSize() = %d, want %d", got, tt.wantWindow)
			}
		})
	}
}

// TestMeterIsFedByTheModelCallback pins the half that is easy to leave out: a
// meter nothing writes to reports zero forever, and compaction that reads zero
// never fires. It asserts through the assembled agent's callback chain rather
// than by calling the meter directly, because the wiring is what breaks.
func TestMeterIsFedByTheModelCallback(t *testing.T) {
	ag := newEmbeddedAgent(t, WithContextWindow(200_000))

	feed := func(promptTokens int32) {
		t.Helper()
		cb := autocompact.MeterCallback(ag.meter)
		if _, err := cb(nil, &adkmodel.LLMResponse{
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: promptTokens},
		}, nil); err != nil {
			t.Fatalf("meter callback: %v", err)
		}
	}

	feed(11_045)
	if got := ag.meter.BodyTokens(); got != 0 {
		t.Errorf("BodyTokens() after the first response = %d, want 0 — it is all prefix", got)
	}
	feed(109_498)
	if got := ag.meter.BodyTokens(); got != 98_453 {
		t.Errorf("BodyTokens() = %d, want 98453 — growth past the prefix", got)
	}
}

// TestContextWindowFor covers the precedence between the option and
// config.json. The option wins: an embedder who states a window in Go has said
// something more specific than a file the user happens to have.
func TestContextWindowFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		cfg  config.Config
		opts options
		want int64
	}{
		{desc: "neither set means unknown", want: 0},
		{desc: "config alone", cfg: config.Config{ContextWindow: 128_000}, want: 128_000},
		{desc: "option alone", opts: options{contextWindow: 200_000}, want: 200_000},
		{
			desc: "option beats config",
			cfg:  config.Config{ContextWindow: 128_000},
			opts: options{contextWindow: 1_048_576},
			want: 1_048_576,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			if got := contextWindowFor(tt.cfg, tt.opts); got != tt.want {
				t.Errorf("contextWindowFor() = %d, want %d", got, tt.want)
			}
		})
	}
}
