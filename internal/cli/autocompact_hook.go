package cli

import (
	"context"
	"fmt"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/logger"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/tools"

	llmmodel "google.golang.org/adk/v2/model"
)

// autoCompactDeps are the pieces a pre-turn compaction pass needs.
type autoCompactDeps struct {
	SessionSvc *pisession.FileService
	Tracker    *guardrail.Tracker
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

// buildAutoCompactHook returns a pre-turn hook that runs the two-stage
// compaction. It reads the body size — context after the stable cached prefix —
// from the token tracker, so a large but fully cached system prompt and tool
// block never trigger compaction on their own.
//
// The hook never aborts a turn on compaction failure: being unable to reclaim
// context is a degraded state, not a reason to refuse the user's request. The
// error is reported and the turn proceeds.
func buildAutoCompactHook(deps autoCompactDeps) agent.PreTurnHook {
	if deps.SessionSvc == nil || deps.Tracker == nil || !deps.Cfg.Enabled {
		return nil
	}

	return func(ctx context.Context, sessionID string) error {
		if sessionID == "" {
			return nil
		}

		body := deps.Tracker.BodyTokens()
		window := deps.Tracker.ContextWindowSize()
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

		// A summarizing rebuild replaces the whole window, so the cached prefix
		// baseline and every dedup pointer into the old history are stale.
		if outcome.Action == pisession.CompactionSummarize {
			deps.Tracker.ResetContextWindow()
			deps.Deduper.Reset()
		}

		deps.report(outcome.String())
		return nil
	}
}

func (d autoCompactDeps) report(msg string) {
	if d.Log != nil {
		d.Log.Info("auto-compact: " + msg)
	}
	if d.Notify != nil {
		d.Notify(msg)
	}
}
