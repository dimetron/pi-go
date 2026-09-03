package ratelimit

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/dimetron/pi-go/internal/retry"
)

// bytesPerToken converts a serialized request body to an input-token estimate.
//
// Measured, not assumed. 22 requests were driven through the local
// agentgateway with `pi --mode print --trace-http` against
// agentgateway/gemini-3.8-flash, pairing each request's body length from the
// httplog entry with the gen_ai.usage.input_tokens the gateway logged for that
// same request; runs were tagged with a nonce in the prompt so the pairing is
// exact rather than by timestamp. Bodies spanned 47 KB to 605 KB, 11k to 220k
// input tokens:
//
//	all 22 samples          min 2.71   median 2.79   max 4.27
//	>=50k tokens (n=20)     min 2.71   median 2.75   max 3.12
//
// 2.7 is the conservative end of the range that matters. The ratio is not
// constant — a first request is mostly system prompt and tool definitions and
// runs near 4.3, but it falls and settles just above 2.7 once source code
// dominates the payload, which is exactly the regime that exhausts a token
// quota. Picking the low end over-charges the small requests, and that is the
// direction to be wrong in: under-counting causes the 429s this package
// exists to prevent, over-counting only costs throughput.
//
// This started as 4 — the ratio internal/memory and internal/tui use for plain
// text — and that was a real defect rather than a rounding error. At the
// measured 2.75, the 2,005,778 tokens that drew the original rejection were
// about 5.5 MB on the wire, which a divisor of 4 estimates at 1.38M: under the
// budget, so the limiter would have paced nothing and 429'd anyway.
const bytesPerToken = 2.7

// hintBodyLimit bounds how much of an error body is scanned for a retry hint.
// Rate-limit bodies are a few hundred bytes; the cap is only there so a
// provider that answers 429 with something enormous cannot be buffered whole.
const hintBodyLimit = 8 << 10

// Transport paces every request through a Limiter, and feeds the server's own
// retry hints back into it.
//
// It is an http.RoundTripper rather than a wrapper around each provider's
// Generate because that is the one place every provider agrees on: pi-go
// reaches Gemini through four different code paths (the genai client, the
// OpenAI Chat Completions client, the Responses client, and an OpenAI-
// compatible local gateway), and all four end up here.
type Transport struct {
	Base    http.RoundTripper
	Limiter *Limiter
}

// RoundTrip waits for budget, sends, and records any cooldown the server asked
// for.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if err := t.Limiter.Wait(req.Context(), estimateRequestTokens(req)); err != nil {
		return nil, err
	}

	resp, err := base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	// 503 is included alongside 429 because an overloaded provider sends
	// Retry-After with it too, and ignoring that hint re-sends into the same
	// overload.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
		if d, ok := retryHint(resp); ok {
			t.Limiter.Backoff(d)
		}
	}
	return resp, nil
}

// estimateRequestTokens approximates the input tokens a request will be
// charged for.
//
// It reads ContentLength rather than the body: the SDKs marshal a request into
// a bytes.Reader, so the length is known before the send and no buffering is
// needed. A body of unknown length (chunked, ContentLength -1) yields 0, which
// spends no token budget — counting it would mean draining and replaying the
// stream, and no provider pi-go talks to streams its request up.
func estimateRequestTokens(req *http.Request) int {
	if req == nil || req.ContentLength <= 0 {
		return 0
	}
	return int(float64(req.ContentLength) / bytesPerToken)
}

// retryHint extracts how long the server asked us to wait.
//
// The Retry-After header is checked first because it is unambiguous, but most
// of the time it is absent and the figure is only in the body: Gemini answers
// with "Please retry in 10.419145242s." and a RetryInfo{retryDelay: 11s}
// detail, and reading it is the difference between one well-timed retry and a
// backoff schedule that keeps landing inside the same exhausted window.
//
// Reading the body here is what closes the gap on pi-go's OpenAI-compatible
// paths specifically. openai-go discards the body when a *stream* fails, so
// the error that reaches the retry loop is the bare
// `POST "…/v1/chat/completions": 429 Too Many Requests` — no quota, no hint,
// nothing for retry.ServerDelay to match. The transport is upstream of that
// discard and still has the body, so it is the last place the hint exists.
func retryHint(resp *http.Response) (time.Duration, bool) {
	if d, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
		return d, true
	}
	if resp.Body == nil {
		return 0, false
	}
	prefix, err := io.ReadAll(io.LimitReader(resp.Body, hintBodyLimit))
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, false
	}
	// Put the bytes back. The SDK still has to decode this body to build the
	// error the user sees, so consuming it without restoring it would trade a
	// "429, quota X exceeded" message for an unexplained decode failure.
	resp.Body = &restoredBody{Reader: io.MultiReader(bytes.NewReader(prefix), resp.Body), closer: resp.Body}
	return retry.ServerDelay(errors.New(string(prefix)))
}

// parseRetryAfter reads the header's two legal forms: delta-seconds, and an
// HTTP-date to wait until.
func parseRetryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if secs <= 0 {
			return 0, false
		}
		return time.Duration(secs * float64(time.Second)), true
	}
	if when, err := http.ParseTime(v); err == nil {
		if d := time.Until(when); d > 0 {
			return d, true
		}
	}
	return 0, false
}

// restoredBody re-presents a body whose prefix was read, keeping the original
// Close so the connection is still released.
type restoredBody struct {
	io.Reader
	closer io.Closer
}

func (b *restoredBody) Close() error { return b.closer.Close() }
