package provider

import (
	"context"
	"errors"
	"iter"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/retry"
)

// scriptedLLM replays one canned outcome per call, so a test can make the
// first attempt fail and the next succeed.
type scriptedLLM struct {
	calls    int
	outcomes [][]scriptedYield
}

type scriptedYield struct {
	resp *model.LLMResponse
	err  error
}

func (s *scriptedLLM) Name() string { return "scripted" }

func (s *scriptedLLM) GenerateContent(context.Context, *model.LLMRequest, bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		i := s.calls
		s.calls++
		if i >= len(s.outcomes) {
			return
		}
		for _, y := range s.outcomes[i] {
			if !yield(y.resp, y.err) {
				return
			}
		}
	}
}

// rateLimited is the shape a Gemini per-minute quota rejection arrives in: a
// content-less response carrying an error code, with the server naming the
// window it reopens in.
//
// The message is a real one, kept verbatim, because its wording is the whole
// difficulty. It reads like terminal billing failure ("exceeded your current
// quota", "check your plan and billing details") while actually being a
// per-minute token limit that clears in seconds — and the retry window it
// names is what internal/retry keys on to tell the two apart.
func rateLimited() *model.LLMResponse {
	return &model.LLMResponse{
		ErrorCode: "429",
		ErrorMessage: "You exceeded your current quota, please check your plan and billing details. " +
			"Quota exceeded for metric: " +
			"generativelanguage.googleapis.com/generate_content_paid_tier_input_token_count, " +
			"limit: 2000000, model: gemini-3.8-flash Please retry in 13.380362265s., " +
			"Status: RESOURCE_EXHAUSTED",
	}
}

func TestRateLimitedFixtureIsTreatedAsTransient(t *testing.T) {
	// Guards the fixture above: if this message ever stopped being classified
	// as transient, every retry test here would still pass while the bug they
	// cover came back.
	err := streamFailure(rateLimited(), nil)
	if !retry.IsTransient(err) {
		t.Fatalf("IsTransient(%v) = false, want true", err)
	}
	d, ok := retry.ServerDelay(err)
	if !ok {
		t.Fatal("ServerDelay found no window in the quota message")
	}
	if d < 13*time.Second || d > 14*time.Second {
		t.Errorf("ServerDelay = %v, want the ~13.38s the server asked for", d)
	}
}

func drain(t *testing.T, seq iter.Seq2[*model.LLMResponse, error]) ([]*model.LLMResponse, []error) {
	t.Helper()
	var resps []*model.LLMResponse
	var errs []error
	for r, err := range seq {
		resps = append(resps, r)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return resps, errs
}

func TestGeminiRetryModelRetriesRateLimitBeforeOutput(t *testing.T) {
	inner := &scriptedLLM{outcomes: [][]scriptedYield{
		{{resp: rateLimited()}},
		{{resp: textResponse("recovered")}},
	}}
	m := geminiRetryModel{inner: inner}

	resps, errs := drain(t, m.GenerateContent(context.Background(), &model.LLMRequest{}, true))

	if inner.calls != 2 {
		t.Fatalf("inner called %d times, want the request re-sent once", inner.calls)
	}
	if len(errs) != 0 {
		t.Fatalf("errors = %v, want the retry to hide the rate limit", errs)
	}
	if len(resps) != 1 || resps[0].Content.Parts[0].Text != "recovered" {
		t.Fatalf("responses = %+v, want only the recovered reply", resps)
	}
}

func TestGeminiRetryModelCommitsOnceOutputIsEmitted(t *testing.T) {
	// A failure that lands after text has reached the consumer cannot be
	// replayed: the caller has already seen part of the answer.
	inner := &scriptedLLM{outcomes: [][]scriptedYield{
		{{resp: textResponse("partial")}, {resp: rateLimited()}},
		{{resp: textResponse("must not happen")}},
	}}
	m := geminiRetryModel{inner: inner}

	resps, _ := drain(t, m.GenerateContent(context.Background(), &model.LLMRequest{}, true))

	if inner.calls != 1 {
		t.Fatalf("inner called %d times, want no retry after partial output", inner.calls)
	}
	if len(resps) != 2 || resps[0].Content.Parts[0].Text != "partial" {
		t.Fatalf("responses = %+v, want the partial reply then the failure", resps)
	}
	if resps[1].ErrorCode != "429" {
		t.Errorf("second response = %+v, want the rate limit passed through", resps[1])
	}
}

func TestGeminiRetryModelDoesNotRetryNonStreaming(t *testing.T) {
	// A non-streaming call is left to the SDK's own retry.
	inner := &scriptedLLM{outcomes: [][]scriptedYield{
		{{resp: rateLimited()}},
		{{resp: textResponse("must not happen")}},
	}}
	m := geminiRetryModel{inner: inner}

	resps, _ := drain(t, m.GenerateContent(context.Background(), &model.LLMRequest{}, false))

	if inner.calls != 1 {
		t.Fatalf("inner called %d times, want a single non-streaming call", inner.calls)
	}
	if len(resps) != 1 || resps[0].ErrorCode != "429" {
		t.Fatalf("responses = %+v, want the failure passed through", resps)
	}
}

func TestGeminiRetryModelStopsWhenConsumerStops(t *testing.T) {
	inner := &scriptedLLM{outcomes: [][]scriptedYield{
		{{resp: textResponse("one")}, {resp: textResponse("two")}},
	}}
	m := geminiRetryModel{inner: inner}

	var seen int
	for range m.GenerateContent(context.Background(), &model.LLMRequest{}, true) {
		seen++
		break
	}
	if seen != 1 {
		t.Fatalf("saw %d responses, want the range to stop at one", seen)
	}
	if inner.calls != 1 {
		t.Errorf("inner called %d times, want one", inner.calls)
	}
}

func TestGeminiRetryModelGivesUpAfterBudget(t *testing.T) {
	// streamRetry is shrunk to one retry in TestMain, so two failures exhaust
	// it and the last failure reaches the caller.
	inner := &scriptedLLM{outcomes: [][]scriptedYield{
		{{resp: rateLimited()}},
		{{resp: rateLimited()}},
		{{resp: textResponse("never reached")}},
	}}
	m := geminiRetryModel{inner: inner}

	resps, _ := drain(t, m.GenerateContent(context.Background(), &model.LLMRequest{}, true))

	if inner.calls != 2 {
		t.Fatalf("inner called %d times, want the budget to stop at 2", inner.calls)
	}
	if len(resps) != 1 || resps[0].ErrorCode != "429" {
		t.Fatalf("responses = %+v, want the final rate limit surfaced", resps)
	}
}

func TestGeminiRetryModelPassesThroughGoErrors(t *testing.T) {
	// A non-transient Go error is not worth re-sending.
	want := errors.New("malformed request")
	inner := &scriptedLLM{outcomes: [][]scriptedYield{
		{{err: want}},
		{{resp: textResponse("must not happen")}},
	}}
	m := geminiRetryModel{inner: inner}

	_, errs := drain(t, m.GenerateContent(context.Background(), &model.LLMRequest{}, true))

	if inner.calls != 1 {
		t.Fatalf("inner called %d times, want no retry for a terminal error", inner.calls)
	}
	if len(errs) != 1 || !errors.Is(errs[0], want) {
		t.Fatalf("errors = %v, want %v passed through", errs, want)
	}
}

func TestGeminiRetryModelName(t *testing.T) {
	if got := (geminiRetryModel{inner: &scriptedLLM{}}).Name(); got != "scripted" {
		t.Errorf("Name() = %q, want the wrapped model's name", got)
	}
}
