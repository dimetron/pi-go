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
// produced anything: five retries, paced 3s, 5s, 7s, 15s, 30s. It retries a
// single HTTP request, so it runs inside whatever timeout the caller already
// set for the turn. A server-supplied rate-limit window still overrides the
// pace.
//
// The schedule is two schedules, because it serves two populations of failure.
//
// The first three delays are short and linear, unchanged, for the faults this
// was originally built for: a dropped socket, a reset stream, a 5xx blip.
// Those clear in seconds, and waiting longer only makes a recoverable turn
// feel broken.
//
// The last two are long because the other population is a per-minute quota
// window, and a retry that lands inside the window that just rejected it is a
// wasted attempt. Cumulatively the five attempts go out at roughly 3s, 8s,
// 15s, 30s and 60s after the first failure, so the budget is not spent before
// the window has had a chance to reopen — which is what three retries totaling
// 15s did against a Gemini rejection asking for 11s. 30s is also the largest
// delay worth naming here: retry.DefaultConfig caps any wait at 60s, so a
// longer entry would be clamped rather than honored.
//
// Five also matches retry.DefaultConfig's MaxRetries, so the per-request and
// per-run budgets no longer disagree about how many attempts a failure gets.
//
// It is a package variable so the provider tests, which drive real error
// responses through httptest servers, can shrink the pauses instead of paying
// the full backoff in wall clock. See TestMain.
var streamRetry = defaultStreamRetry()

func defaultStreamRetry() retry.Config {
	cfg := retry.DefaultConfig()
	cfg.MaxRetries = 5
	cfg.Delays = []time.Duration{
		3 * time.Second, 5 * time.Second, 7 * time.Second,
		15 * time.Second, 30 * time.Second,
	}
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

		delay := retry.Delay(cfg, attempt, cause)
		retry.Notify(ctx, retry.Attempt{
			Attempt:    attempt + 1,
			MaxRetries: cfg.MaxRetries,
			Delay:      delay,
			Err:        cause,
		})
		if !sleepCtx(ctx, delay) {
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
