// Package ctxwindow resolves the context window of the model a session runs
// against, which is the denominator every compaction threshold is measured in.
//
// It is deliberately separate from internal/autocompact, which consumes the
// number: resolving a window means asking internal/provider, and piagent is
// barred by TestPiagentStaysIsolated from reaching provider construction even
// transitively. Keeping the two apart lets piagent install the same compaction
// hook as the CLI without acquiring that dependency.
package ctxwindow

import (
	"context"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

// Resolve reports the context window the running model actually
// has, in tokens. It starts from the embedded catalog and lets the two
// providers that can be asked at runtime — Ollama and OpenRouter — correct it.
//
// A zero result means the window is unknown, and compaction then never fires:
// AutoCompactConfig.Decide treats an unknown window as CompactionNone rather
// than guessing a percentage of nothing. Setting context_window in config.json
// is the escape hatch, and it wins over every other source, because the
// embedded catalog does not cover every provider's models.
func Resolve(ctx context.Context, cfg config.Config, info provider.Info, baseURL string) int64 {
	ctxWindowSize := provider.ContextWindowSizeFor(info.Provider, info.Model)
	if info.Ollama {
		if n := provider.OllamaContextWindowSize(ctx, baseURL, info.Model); n > 0 {
			ctxWindowSize = n
		}
	}
	if info.Provider == "openrouter" {
		if n := provider.OpenRouterContextWindowSize(ctx, baseURL, info.Model); n > 0 {
			ctxWindowSize = n
		}
	}
	if cfg.ContextWindow > 0 {
		ctxWindowSize = cfg.ContextWindow
	}
	return ctxWindowSize
}
