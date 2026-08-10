package provider

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/retry"
)

// TestMain shrinks the stream-retry budget for the whole package. Several
// existing tests drive genuine 5xx responses through httptest servers, and
// those are transient by design, so every one of them now re-sends. Left at
// production settings they sit through the backoff *and* through the vendor
// SDK's own internal retry on each attempt, which added ~30s to the suite.
//
// The retry behavior itself is covered by the tests below, which pass their
// own explicit configs to retryStream, so nothing is lost by trimming here.
func TestMain(m *testing.M) {
	streamRetry = retry.Config{
		MaxRetries:   1,
		InitialDelay: time.Millisecond,
		MaxDelay:     2 * time.Millisecond,
	}
	os.Exit(m.Run())
}

// fastRetry keeps the sleeps out of the test's wall clock while leaving the
// attempt budget intact.
func fastRetry(maxRetries int) retry.Config {
	return retry.Config{
		MaxRetries:   maxRetries,
		InitialDelay: time.Millisecond,
		MaxDelay:     2 * time.Millisecond,
	}
}

func textResponse(s string) *model.LLMResponse {
	return &model.LLMResponse{
		Partial: true,
		Content: &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: s}}},
	}
}

func streamError(msg string) *model.LLMResponse {
	return &model.LLMResponse{ErrorCode: "STREAM_ERROR", ErrorMessage: msg}
}

// collectStream drains a retryStream run into the responses it forwarded.
func collectStream(ctx context.Context, t *testing.T, cfg retry.Config, attempt streamAttempt) []*model.LLMResponse {
	t.Helper()
	var got []*model.LLMResponse
	retryStream(ctx, cfg, func(resp *model.LLMResponse, err error) bool {
		if err != nil {
			got = append(got, &model.LLMResponse{ErrorCode: "GO_ERROR", ErrorMessage: err.Error()})
			return true
		}
		got = append(got, resp)
		return true
	}, attempt)
	return got
}

// The regression this whole change exists for: a rate limit that arrives before
// any content must be re-sent, not surfaced.
func TestRetryStreamRetriesRateLimitBeforeContent(t *testing.T) {
	const rateLimit = `received error while streaming: {"code":"rate_limit_exceeded",` +
		`"message":"Rate limit reached on tokens per min (TPM). Please try again in 1ms."}`

	calls := 0
	got := collectStream(context.Background(), t, fastRetry(3), func(y func(*model.LLMResponse, error) bool) {
		calls++
		if calls < 3 {
			y(streamError(rateLimit), nil)
			return
		}
		y(textResponse("recovered"), nil)
	})

	if calls != 3 {
		t.Errorf("attempts = %d, want 3", calls)
	}
	if len(got) != 1 {
		t.Fatalf("forwarded %d responses, want 1", len(got))
	}
	if got[0].ErrorCode != "" {
		t.Errorf("forwarded an error to the caller: %+v", got[0])
	}
}

// Quota exhaustion also arrives as a 429. Retrying it only delays a failure the
// user has to fix, so it must pass through on the first attempt.
func TestRetryStreamDoesNotRetryQuotaExhaustion(t *testing.T) {
	const quota = "429 Too Many Requests: you (dimetron) have reached your weekly " +
		"usage limit, upgrade for higher limits: https://ollama.com/upgrade"

	calls := 0
	got := collectStream(context.Background(), t, fastRetry(3), func(y func(*model.LLMResponse, error) bool) {
		calls++
		y(streamError(quota), nil)
	})

	if calls != 1 {
		t.Errorf("attempts = %d, want 1 (quota is terminal)", calls)
	}
	if len(got) != 1 || got[0].ErrorMessage != quota {
		t.Errorf("want the original error forwarded once, got %+v", got)
	}
}

// Once text has reached the caller the attempt is committed: replaying it would
// duplicate the turn's output.
func TestRetryStreamDoesNotRetryAfterContent(t *testing.T) {
	calls := 0
	got := collectStream(context.Background(), t, fastRetry(3), func(y func(*model.LLMResponse, error) bool) {
		calls++
		y(textResponse("partial"), nil)
		y(streamError("503 service unavailable"), nil)
	})

	if calls != 1 {
		t.Errorf("attempts = %d, want 1 (content already emitted)", calls)
	}
	if len(got) != 2 {
		t.Fatalf("forwarded %d responses, want 2", len(got))
	}
	if got[1].ErrorCode != "STREAM_ERROR" {
		t.Errorf("want the stream error forwarded, got %+v", got[1])
	}
}

func TestRetryStreamExhaustsBudget(t *testing.T) {
	calls := 0
	got := collectStream(context.Background(), t, fastRetry(2), func(y func(*model.LLMResponse, error) bool) {
		calls++
		y(streamError("503 service unavailable"), nil)
	})

	if calls != 3 {
		t.Errorf("attempts = %d, want 3 (initial + 2 retries)", calls)
	}
	if len(got) != 1 || got[0].ErrorMessage != "503 service unavailable" {
		t.Errorf("want the last error forwarded, got %+v", got)
	}
}

// A Go error on the error channel is the non-streaming shape, and must be
// retried on the same terms.
func TestRetryStreamRetriesGoError(t *testing.T) {
	calls := 0
	got := collectStream(context.Background(), t, fastRetry(3), func(y func(*model.LLMResponse, error) bool) {
		calls++
		if calls < 2 {
			y(nil, errors.New("connection reset by peer"))
			return
		}
		y(textResponse("ok"), nil)
	})

	if calls != 2 {
		t.Errorf("attempts = %d, want 2", calls)
	}
	if len(got) != 1 || got[0].ErrorCode != "" {
		t.Errorf("want one clean response, got %+v", got)
	}
}

func TestRetryStreamSuccessIsNotRetried(t *testing.T) {
	calls := 0
	got := collectStream(context.Background(), t, fastRetry(3), func(y func(*model.LLMResponse, error) bool) {
		calls++
		y(textResponse("a"), nil)
		y(textResponse("b"), nil)
	})

	if calls != 1 {
		t.Errorf("attempts = %d, want 1", calls)
	}
	if len(got) != 2 {
		t.Errorf("forwarded %d responses, want 2", len(got))
	}
}

// The same guarantee at the other observation point: a cancel that lands
// before the backoff even starts. Canceling inside the attempt makes this
// deterministic — ctx is already canceled when retryStream inspects it — where
// TestRetryStreamCancelDuringBackoff races the cancel against the sleep and so
// can exercise either path.
func TestRetryStreamCancelBeforeBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	got := collectStream(ctx, t, fastRetry(3), func(y func(*model.LLMResponse, error) bool) {
		calls++
		cancel()
		y(streamError("503 service unavailable"), nil)
	})

	if calls != 1 {
		t.Errorf("attempts = %d, want 1 (canceled)", calls)
	}
	if len(got) != 1 {
		t.Fatalf("forwarded %d responses, want 1", len(got))
	}
	if got[0].ErrorCode != "" {
		t.Errorf("want a cancellation, got error %+v", got[0])
	}
	if !got[0].TurnComplete {
		t.Error("want the canceled response to complete the turn")
	}
}

// Canceling mid-wait must end the turn the way Esc ends it, not by reporting
// the rate limit the user never needs to see. The cancel races the backoff, so
// retryStream may notice it either before the sleep or during it; both are
// cancellations, and the assertions below hold whichever one wins.
func TestRetryStreamCancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := retry.Config{MaxRetries: 3, InitialDelay: time.Hour, MaxDelay: time.Hour}

	started := make(chan struct{})
	done := make(chan []*model.LLMResponse, 1)
	go func() {
		done <- collectStream(ctx, t, cfg, func(y func(*model.LLMResponse, error) bool) {
			close(started)
			y(streamError("503 service unavailable"), nil)
		})
	}()

	// The first attempt has to land before the cancel, or there is no backoff
	// to interrupt. cfg retries only once here, so started is closed once.
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first attempt never ran")
	}
	cancel()

	select {
	case got := <-done:
		if len(got) != 1 {
			t.Fatalf("forwarded %d responses, want 1", len(got))
		}
		if got[0].ErrorCode != "" {
			t.Errorf("want a cancellation, got error %+v", got[0])
		}
		if !got[0].TurnComplete {
			t.Error("want the canceled response to complete the turn")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retryStream did not return after cancel")
	}
}

// A caller that stops consuming (Esc, or a downstream break) must stop the
// retry loop too rather than re-sending the request.
func TestRetryStreamStopsWhenCallerHalts(t *testing.T) {
	calls := 0
	retryStream(context.Background(), fastRetry(3), func(*model.LLMResponse, error) bool {
		return false
	}, func(y func(*model.LLMResponse, error) bool) {
		calls++
		y(textResponse("a"), nil)
		y(streamError("503 service unavailable"), nil)
	})

	if calls != 1 {
		t.Errorf("attempts = %d, want 1", calls)
	}
}

func TestStreamFailure(t *testing.T) {
	if got := streamFailure(textResponse("hi"), nil); got != nil {
		t.Errorf("content response should not be a failure, got %v", got)
	}
	if got := streamFailure(nil, nil); got != nil {
		t.Errorf("nil pair should not be a failure, got %v", got)
	}
	if got := streamFailure(&model.LLMResponse{ErrorCode: "API_ERROR"}, nil); got == nil || got.Error() != "API_ERROR" {
		t.Errorf("bare code should surface as the message, got %v", got)
	}
	got := streamFailure(&model.LLMResponse{ErrorCode: "DAILY_LIMIT_EXCEEDED", ErrorMessage: "over budget"}, nil)
	if got == nil || got.Error() != "DAILY_LIMIT_EXCEEDED: over budget" {
		t.Errorf("want code and message, got %v", got)
	}
}
