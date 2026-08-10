package provider

import (
	"context"
	"errors"
	"time"

	"google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/retry"
)

// streamAttempt runs one streaming request, forwarding responses to yield.
type streamAttempt func(yield func(*model.LLMResponse, error) bool)

// streamRetry is the budget for re-sending a request that died before it
// produced anything. It is deliberately smaller than the agent-level budget:
// this retries a single HTTP request, so it runs inside whatever timeout the
// caller already set for the turn.
//
// It is a package variable so the provider tests, which drive real error
// responses through httptest servers, can shrink the pauses instead of paying
// the full backoff in wall clock. See TestMain.
var streamRetry = defaultStreamRetry()

func defaultStreamRetry() retry.Config {
	cfg := retry.DefaultConfig()
	cfg.MaxRetries = 3
	return cfg
}

// streamRetryConfig returns the current stream-retry budget.
func streamRetryConfig() retry.Config { return streamRetry }

// retryStream re-sends a streaming request that failed transiently *before*
// emitting anything.
//
// The "before emitting anything" condition is what makes this safe, and it is
// why the retry lives here rather than around the agent run. A provider reports
// a stream failure as a content-less LLMResponse carrying ErrorCode — see
// oaiRunStreaming — with a nil Go error, so nothing upstream can tell a failed
// request from a finished one. Catching it at the source means a rate-limited
// request is re-sent on its own, with the turn's tool calls and partial text
// left untouched; once any of those have been forwarded, the attempt is
// committed and the error passes straight through.
func retryStream(ctx context.Context, cfg retry.Config, yield func(*model.LLMResponse, error) bool, attemptFn streamAttempt) {
	for attempt := 0; ; attempt++ {
		var (
			emitted  bool
			failResp *model.LLMResponse
			failErr  error
			halted   bool
		)

		attemptFn(func(resp *model.LLMResponse, err error) bool {
			if failure := streamFailure(resp, err); failure != nil && !emitted {
				failResp, failErr = resp, err
				return false
			}
			emitted = true
			if !yield(resp, err) {
				halted = true
				return false
			}
			return true
		})

		if halted {
			return
		}
		cause := streamFailure(failResp, failErr)
		if cause == nil {
			return
		}

		// Cancellation is checked on its own, and before the budget, because a
		// canceled turn ends the way Esc ends it whatever else went wrong: the
		// user is not waiting on the answer any more, so the provider's error
		// is noise. Folding this into the condition below would report that
		// error instead, and which of the two the user saw would depend on
		// whether the cancel landed before this line or during the sleep.
		if ctx.Err() != nil {
			_ = yield(canceledResponse(), nil)
			return
		}

		if attempt >= cfg.MaxRetries || !retry.IsTransient(cause) {
			_ = yield(failResp, failErr)
			return
		}

		if !sleepCtx(ctx, retry.Delay(cfg, attempt, cause)) {
			// Canceled while waiting: same reasoning as above.
			_ = yield(canceledResponse(), nil)
			return
		}
	}
}

// streamFailure reduces a yielded (response, error) pair to the failure it
// describes, or nil if it describes progress.
func streamFailure(resp *model.LLMResponse, err error) error {
	if err != nil {
		return err
	}
	if resp == nil || resp.ErrorCode == "" {
		return nil
	}
	if resp.ErrorMessage == "" {
		return errors.New(resp.ErrorCode)
	}
	// Keep the code alongside the message: named codes such as
	// DAILY_LIMIT_EXCEEDED carry the only signal that a failure is terminal.
	return errors.New(resp.ErrorCode + ": " + resp.ErrorMessage)
}

// sleepCtx waits for d, reporting false if ctx was canceled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
