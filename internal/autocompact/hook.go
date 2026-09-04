// Package autocompact installs pi-go's two-stage context compaction as a
// pre-turn hook: shed superseded tool results at the lower threshold,
// summarize at the upper one.
//
// It lives outside internal/cli so that every agent entry point can install
// the same hook — the interactive TUI, the one-shot CLI, the ACP server and
// the piagent SDK. A long agent session re-sends its whole transcript on every
// turn, so an entry point without compaction pays that growth in full.
package autocompact

import (
	"context"
	"fmt"
	"sync"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/logger"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"

	llmmodel "google.golang.org/adk/v2/model"
)

// ContextMeter reports how much of the model's context window the live
// transcript occupies. *guardrail.Tracker satisfies it, and so does [Meter];
// the interface is what keeps this package off internal/guardrail, which
// piagent may not reach even transitively.
//
// BodyTokens is the figure the thresholds are measured against: tokens after
// the stable cached prefix, so a large but fully-cached system prompt never
// pushes a session toward compaction on its own.
type ContextMeter interface {
	BodyTokens() int64
	ContextWindowSize() int64
	SetLastPromptTokens(int64)
}

// Deps are the pieces a pre-turn compaction pass needs.
type Deps struct {
	SessionSvc *pisession.FileService
	Tracker    ContextMeter
	Deduper    *tools.ResultDeduper
	Cfg        pisession.AutoCompactConfig
	Log        *logger.Logger

	// SummarizerLLM writes the handoff summary. Nil falls back to a degraded
	// placeholder that names the files touched — lossy, but it still reclaims
	// the context rather than leaving the session stuck over budget.
	SummarizerLLM llmmodel.LLM

	// Notify surfaces the outcome to the user. Compaction silently discarding
	// history is exactly the kind of thing that should not be silent.
	Notify func(string)
}

// BuildHook returns a pre-turn hook that runs the two-stage compaction.
// It returns nil when compaction cannot run — no session service, no token
// tracker, or disabled by config — so a caller can install the result
// unconditionally. It reads the body size — context after the stable cached prefix —
// from the token tracker, so a large but fully cached system prompt and tool
// block never trigger compaction on their own.
//
// The hook never aborts a turn on compaction failure: being unable to reclaim
// context is a degraded state, not a reason to refuse the user's request. The
// error is reported and the turn proceeds.
func BuildHook(deps Deps) agent.PreTurnHook {
	if deps.SessionSvc == nil || deps.Tracker == nil || !deps.Cfg.Enabled {
		return nil
	}

	// An unknown window is reported once rather than every turn: the hook is
	// inert for the whole session in that state, and repeating it each turn
	// would bury the notice it is trying to deliver.
	var warnUnknownWindow sync.Once

	return func(ctx context.Context, sessionID string) error {
		if sessionID == "" {
			return nil
		}

		body := deps.Tracker.BodyTokens()
		window := deps.Tracker.ContextWindowSize()
		if window <= 0 {
			// Compaction has no denominator, so it can never fire. Saying so
			// beats the alternative, which is a transcript that grows until
			// the provider rejects it with nothing having warned anyone.
			warnUnknownWindow.Do(func() {
				deps.report("context window unknown for this model — auto-compaction is off; set context_window in ~/.pi-go/config.json")
			})
			return nil
		}
		if deps.Cfg.Decide(body, window) == pisession.CompactionNone {
			return nil
		}

		var summarizer pisession.Summarizer
		if deps.SummarizerLLM != nil {
			summarizer = pisession.LLMSummarizer(ctx, deps.SummarizerLLM)
		} else {
			summarizer = pisession.SimpleSummarizer
		}

		outcome, err := deps.SessionSvc.AutoCompact(
			sessionID, agent.AppName, agent.DefaultUserID,
			body, window, deps.Cfg, summarizer,
		)
		if err != nil {
			msg := fmt.Sprintf("Auto-compaction failed: %v (continuing without reclaiming context)", err)
			deps.report(msg)
			return nil
		}
		if outcome.Action == pisession.CompactionNone {
			return nil
		}

		// Push the post-compaction token count back into the tracker so the
		// bottom context gauge reflects the reclaimed window on the next
		// render, rather than reading the pre-compaction number until the
		// next LLM response arrives. A summarizing rebuild also invalidates
		// dedup pointers into the old conversation, so the deduper must be
		// reset alongside it.
		//
		// SetLastPromptTokens also resets the cached-prefix baseline: the new
		// window has, by definition, a new stable prefix, and prior prefix
		// lengths no longer apply. (The bare ResetContextWindow we used to
		// call only on summarize missed the shed path entirely.)
		if int64(outcome.TokensAfter) >= 0 {
			deps.Tracker.SetLastPromptTokens(int64(outcome.TokensAfter))
		}
		if outcome.Action == pisession.CompactionSummarize {
			deps.Deduper.Reset()
		}

		deps.report(outcome.String())
		return nil
	}
}

func (d Deps) report(msg string) {
	if d.Log != nil {
		d.Log.Info("auto-compact: " + msg)
	}
	if d.Notify != nil {
		d.Notify(msg)
	}
}
