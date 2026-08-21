package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/dimetron/pi-go/internal/extension"
)

// updateContextUsageGolden regenerates testdata/context_usage/*.golden.
// Run with: go test ./internal/tui/ -run ContextUsageGolden -update-context-usage
var updateContextUsageGolden = flag.Bool("update-context-usage", false,
	"rewrite the /context golden files from the current implementation")

// contextUsageCase pins one /context rendering. The set is chosen to reach
// every branch of formatContextUsage: the breakdown gauge with and without a
// tracker, the daily-usage bar with and without a limit, the context-window
// section with a known and an unknown window, the cache lines at zero and at
// non-zero, plus the skills and compaction sections.
type contextUsageCase struct {
	name  string
	build func() *model
}

// chatMessages builds a transcript with one message of each role, sized so the
// per-role token estimates differ from each other.
func chatMessages() []message {
	return []message{
		{role: "user", content: strings.Repeat("u", 400)},
		{role: "assistant", content: strings.Repeat("a", 800)},
		{role: "tool", tool: "bash", toolIn: "ls -la", content: strings.Repeat("t", 1200)},
		{role: "user", content: "and one more question"},
	}
}

func contextUsageCases() []contextUsageCase {
	return []contextUsageCase{
		{
			// No tracker, no messages: usedBlocks stays 0 and every optional
			// section is skipped.
			name: "empty",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: nil},
					cfg:       Config{ModelName: "test-model"},
				}
			},
		},
		{
			// No tracker but a short conversation: the nominal-100k branch
			// floors the bar at one block.
			name: "no_tracker_small",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: chatMessages()},
					cfg:       Config{ModelName: "gpt-4", ProviderName: "openai"},
				}
			},
		},
		{
			// Over 10k estimated tokens, so the bar scales against 100k.
			name: "no_tracker_large",
			build: func() *model {
				return &model{
					width: 120,
					chatModel: ChatModel{Messages: []message{
						{role: "user", content: strings.Repeat("x", 90_000)},
						{role: "assistant", content: strings.Repeat("y", 30_000)},
					}},
					cfg: Config{ModelName: "gpt-4", ProviderName: "openai"},
				}
			},
		},
		{
			name: "tracker_limit_full_sections",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: chatMessages()},
					cfg: Config{
						ModelName:    "claude-sonnet",
						ProviderName: "anthropic",
						TokenTracker: fakeTokenTracker{
							limit: 100_000, remaining: 43_210, pctUsed: 56.79,
							totalUsed: 56_790, lastPromptTok: 20_000,
							ctxWindowSize: 200_000, ctxPercentUsed: 10,
							lastCachedTok: 15_000, cachedToday: 123_456,
							cacheHitRate: 42.5, bodyTokens: 4_321,
							cachePrefixTok: 15_679,
						},
					},
				}
			},
		},
		{
			// Tracker present but no limit: the model line falls to the
			// "ctx ~N tokens" form and Remaining is suppressed.
			name: "tracker_no_limit",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: chatMessages()},
					cfg: Config{
						ModelName:    "local-model",
						TokenTracker: fakeTokenTracker{totalUsed: 1_000},
					},
				}
			},
		},
		{
			// Usage past the limit: barFill clamps and the percentage is >100.
			name: "tracker_overflow",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: nil},
					cfg: Config{
						ModelName: "test-model",
						TokenTracker: fakeTokenTracker{
							limit: 100_000, totalUsed: 200_000, pctUsed: 200,
						},
					},
				}
			},
		},
		{
			// Last prompt known, window size unknown.
			name: "ctx_window_unknown",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: chatMessages()},
					cfg: Config{
						ModelName: "test-model",
						TokenTracker: fakeTokenTracker{
							limit: 50_000, totalUsed: 10_000, pctUsed: 20,
							lastPromptTok: 7_500,
						},
					},
				}
			},
		},
		{
			// Prompt larger than the window: free tokens clamp at zero and the
			// free percentage goes negative.
			name: "ctx_window_overfull",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: chatMessages()},
					cfg: Config{
						ModelName: "test-model",
						TokenTracker: fakeTokenTracker{
							limit: 500_000, totalUsed: 260_000, pctUsed: 52,
							lastPromptTok: 250_000, ctxWindowSize: 200_000,
							ctxPercentUsed: 125,
						},
					},
				}
			},
		},
		{
			// Cache section with no hit at all and no stable prefix.
			name: "cache_miss",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: chatMessages()},
					cfg: Config{
						ModelName: "test-model",
						TokenTracker: fakeTokenTracker{
							limit: 100_000, totalUsed: 20_000, pctUsed: 20,
							lastPromptTok: 9_000, ctxWindowSize: 128_000,
							ctxPercentUsed: 7,
						},
					},
				}
			},
		},
		{
			// Breakdown gauge driven by the tracker's reported prompt size.
			name: "breakdown_with_tracker",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: chatMessages()},
					cfg: Config{
						ModelName:        "claude-sonnet",
						ProviderName:     "anthropic",
						ContextBreakdown: sampleBreakdown(),
						TokenTracker: fakeTokenTracker{
							limit: 100_000, totalUsed: 40_000, pctUsed: 40,
							lastPromptTok: 60_000, ctxWindowSize: 200_000,
							ctxPercentUsed: 30,
						},
					},
				}
			},
		},
		{
			// Breakdown with no tracker: used falls back to the message
			// estimate plus fixed overhead, window to autoRangeWindow.
			name: "breakdown_no_tracker",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: chatMessages()},
					cfg:       Config{ModelName: "test-model", ContextBreakdown: sampleBreakdown()},
				}
			},
		},
		{
			// Narrow terminal: the breakdown width is the clamped chat width
			// rather than the 64-cell cap.
			name: "breakdown_narrow",
			build: func() *model {
				return &model{
					width:     44,
					chatModel: ChatModel{Messages: chatMessages()},
					cfg:       Config{ModelName: "test-model", ContextBreakdown: sampleBreakdown()},
				}
			},
		},
		{
			// Skills sort alphabetically; the empty source and empty
			// description fall back to "user" and "(no description)".
			name: "skills",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: nil},
					cfg: Config{
						ModelName: "test-model",
						Skills: []extension.Skill{
							{Name: "zebra", Description: "Last alphabetically", Source: "project"},
							{Name: "alpha", Description: "First skill", Source: "bundled"},
							{Name: "nosource"},
						},
					},
				}
			},
		},
		{
			name: "compact_metrics",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: chatMessages()},
					cfg: Config{
						ModelName:      "test-model",
						CompactMetrics: &mockCompactStats{stats: "- **Saved**: 42% of tool output\n"},
					},
				}
			},
		},
		{
			// Empty stats suppress the whole compaction section.
			name: "compact_metrics_empty",
			build: func() *model {
				return &model{
					width:     120,
					chatModel: ChatModel{Messages: nil},
					cfg: Config{
						ModelName:      "test-model",
						CompactMetrics: &mockCompactStats{stats: ""},
					},
				}
			},
		},
	}
}

func sampleBreakdown() *ContextBreakdown {
	return &ContextBreakdown{
		SystemPrompt: 3_500,
		ToolDefs:     7_200,
		Rules:        1_100,
		Skills:       900,
		MCPTools:     2_400,
		Subagents:    650,
	}
}

// TestFormatContextUsageGolden pins the rendered /context text. The goldens
// hold the ANSI-stripped output so they stay portable across terminals while
// still catching any change to wording, ordering, or column widths.
func TestFormatContextUsageGolden(t *testing.T) {
	t.Parallel()

	for _, tc := range contextUsageCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := ansi.Strip(tc.build().formatContextUsage())
			path := filepath.Join("testdata", "context_usage", tc.name+".golden")

			if *updateContextUsageGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(path) //nolint:gosec // fixed test-local path
			if err != nil {
				t.Fatalf("read golden: %v (rerun with -update-context-usage)", err)
			}
			if got != string(want) {
				t.Errorf("output drifted from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
			}
		})
	}
}
