package ratelimit

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// stubRoundTripper answers every request with a canned response. Used instead
// of httptest so these tests do not bind a local listener — that fails under
// the sandbox for reasons unrelated to what is being tested.
type stubRoundTripper struct {
	resp    *http.Response
	err     error
	calls   int
	lastReq *http.Request
}

func (s *stubRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	s.calls++
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	return s.resp, nil
}

func response(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func newRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, "http://localhost:4000/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	return req
}

func TestEstimateRequestTokens(t *testing.T) {
	t.Parallel()
	req := newRequest(t, strings.Repeat("x", 405))
	if got, want := estimateRequestTokens(req), 150; got != want {
		t.Fatalf("estimateRequestTokens = %d, want %d", got, want)
	}

	// A body of unknown length spends no budget rather than being drained and
	// replayed to count it.
	chunked := newRequest(t, "")
	chunked.ContentLength = -1
	if got := estimateRequestTokens(chunked); got != 0 {
		t.Fatalf("chunked body charged %d tokens, want 0", got)
	}
	if got := estimateRequestTokens(nil); got != 0 {
		t.Fatalf("nil request charged %d tokens, want 0", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header string
		want   time.Duration
		ok     bool
	}{
		{"empty", "", 0, false},
		{"delta seconds", "30", 30 * time.Second, true},
		{"fractional seconds", "1.5", 1500 * time.Millisecond, true},
		{"zero is no hint", "0", 0, false},
		{"negative is no hint", "-5", 0, false},
		{"garbage", "soon", 0, false},
		{"past date", "Mon, 02 Jan 2006 15:04:05 GMT", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseRetryAfter(tt.header)
		if ok != tt.ok || (tt.ok && got != tt.want) {
			t.Errorf("%s: parseRetryAfter(%q) = %v, %v; want %v, %v", tt.name, tt.header, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseRetryAfterHTTPDate(t *testing.T) {
	t.Parallel()
	when := time.Now().UTC().Add(45 * time.Second).Format(http.TimeFormat)
	got, ok := parseRetryAfter(when)
	if !ok {
		t.Fatalf("parseRetryAfter(%q) reported no hint", when)
	}
	// The header has one-second resolution, so allow a couple of seconds slack.
	if got < 42*time.Second || got > 46*time.Second {
		t.Fatalf("parseRetryAfter(%q) = %v, want ~45s", when, got)
	}
}

// The Retry-After header wins when present: it is unambiguous, and cheaper
// than reading the body.
func TestRetryHintPrefersHeader(t *testing.T) {
	t.Parallel()
	h := http.Header{"Retry-After": []string{"7"}}
	resp := response(http.StatusTooManyRequests, h, "Please retry in 42s.")
	got, ok := retryHint(resp)
	if !ok || got != 7*time.Second {
		t.Fatalf("retryHint = %v, %v; want 7s, true", got, ok)
	}
}

// The failure this was written against: Gemini states the wait in the body
// only, and pi-go's OpenAI-compatible paths discard that body before the retry
// loop ever sees it. The transport is the last place the hint exists.
func TestRetryHintReadsGeminiBody(t *testing.T) {
	t.Parallel()
	const body = `{"error":{"code":429,"message":"You exceeded your current quota, ` +
		`please check your plan and billing details. * Quota exceeded for metric: ` +
		`generativelanguage.googleapis.com/generate_content_paid_tier_input_token_count, ` +
		`limit: 2000000, model: gemini-3.8-flash\nPlease retry in 10.419145242s.",` +
		`"status":"RESOURCE_EXHAUSTED"}}`

	resp := response(http.StatusTooManyRequests, nil, body)
	got, ok := retryHint(resp)
	if !ok {
		t.Fatal("retryHint found no hint in a Gemini quota body")
	}
	if got < 10*time.Second || got > 11*time.Second {
		t.Fatalf("retryHint = %v, want ~10.4s", got)
	}

	// The SDK still has to decode this body to build the error the user sees,
	// so reading it for the hint must leave it intact.
	rest, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading restored body: %v", err)
	}
	if string(rest) != body {
		t.Fatalf("body was not restored:\n got %q\nwant %q", rest, body)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("closing restored body: %v", err)
	}
}

func TestRetryHintReadsRetryInfoDetail(t *testing.T) {
	t.Parallel()
	resp := response(http.StatusTooManyRequests, nil,
		`{"details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"11s"}]}`)
	got, ok := retryHint(resp)
	if !ok || got != 11*time.Second {
		t.Fatalf("retryHint = %v, %v; want 11s, true", got, ok)
	}
}

func TestRetryHintAbsent(t *testing.T) {
	t.Parallel()
	resp := response(http.StatusTooManyRequests, nil, `{"error":{"message":"slow down"}}`)
	if got, ok := retryHint(resp); ok {
		t.Fatalf("retryHint = %v, want no hint", got)
	}
}

// A 429 must hold back every caller on the budget, not just the one rejected:
// concurrent callers sharing the quota are what turned one rejection into
// three, 0.7s apart.
func TestTransportBackoffAfter429(t *testing.T) {
	t.Parallel()
	stub := &stubRoundTripper{
		resp: response(http.StatusTooManyRequests, http.Header{"Retry-After": []string{"30"}}, ""),
	}
	lim := New(Limits{RequestsPerMinute: 1000})
	tr := &Transport{Base: stub, Limiter: lim}

	if _, err := tr.RoundTrip(newRequest(t, "hello")); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if remaining := lim.cooldownRemaining(); remaining < 25*time.Second {
		t.Fatalf("cooldown after 429 = %v, want ~30s", remaining)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := newRequest(t, "hello").WithContext(ctx)
	if _, err := tr.RoundTrip(req); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second RoundTrip err = %v, want it held by the cooldown", err)
	}
	if stub.calls != 1 {
		t.Fatalf("stub saw %d calls, want the second request held before sending", stub.calls)
	}
}

func TestTransport503AlsoBacksOff(t *testing.T) {
	t.Parallel()
	stub := &stubRoundTripper{
		resp: response(http.StatusServiceUnavailable, http.Header{"Retry-After": []string{"20"}}, ""),
	}
	lim := New(Limits{RequestsPerMinute: 1000})
	tr := &Transport{Base: stub, Limiter: lim}

	if _, err := tr.RoundTrip(newRequest(t, "hello")); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if remaining := lim.cooldownRemaining(); remaining < 15*time.Second {
		t.Fatalf("cooldown after 503 = %v, want ~20s", remaining)
	}
}

func TestTransportSuccessDoesNotBackOff(t *testing.T) {
	t.Parallel()
	stub := &stubRoundTripper{resp: response(http.StatusOK, nil, `{"ok":true}`)}
	lim := New(Limits{RequestsPerMinute: 1000})
	tr := &Transport{Base: stub, Limiter: lim}

	if _, err := tr.RoundTrip(newRequest(t, "hello")); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if remaining := lim.cooldownRemaining(); remaining > 0 {
		t.Fatalf("a 200 set a %v cooldown", remaining)
	}
}

// The wait happens before the request is sent, not after it is rejected.
func TestTransportPacesBeforeSending(t *testing.T) {
	t.Parallel()
	stub := &stubRoundTripper{resp: response(http.StatusOK, nil, "")}
	// 400 bytes of body is 100 estimated tokens, so a 100-token budget admits
	// exactly one request per minute.
	lim := New(Limits{InputTokensPerMinute: 100})
	tr := &Transport{Base: stub, Limiter: lim}

	body := strings.Repeat("x", 400)
	if _, err := tr.RoundTrip(newRequest(t, body)); err != nil {
		t.Fatalf("first RoundTrip: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := tr.RoundTrip(newRequest(t, body).WithContext(ctx)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second RoundTrip err = %v, want it paced", err)
	}
	if stub.calls != 1 {
		t.Fatalf("stub saw %d calls, want 1", stub.calls)
	}
}

// A nil Limiter is the "unpaced" configuration and must pass requests straight
// through.
func TestTransportNilLimiterPassesThrough(t *testing.T) {
	t.Parallel()
	stub := &stubRoundTripper{resp: response(http.StatusOK, nil, "")}
	tr := &Transport{Base: stub}
	for range 5 {
		if _, err := tr.RoundTrip(newRequest(t, "hello")); err != nil {
			t.Fatalf("RoundTrip: %v", err)
		}
	}
	if stub.calls != 5 {
		t.Fatalf("stub saw %d calls, want 5", stub.calls)
	}
}

func TestTransportPropagatesError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("dial failed")
	stub := &stubRoundTripper{err: wantErr}
	tr := &Transport{Base: stub, Limiter: New(Limits{RequestsPerMinute: 100})}

	if _, err := tr.RoundTrip(newRequest(t, "hello")); !errors.Is(err, wantErr) {
		t.Fatalf("RoundTrip err = %v, want %v", err, wantErr)
	}
}

// The estimate must not under-count against what the gateway actually charged.
// These are the paired samples the bytesPerToken comment cites — body bytes
// measured from the httplog entry, input tokens from the gateway's log line
// for that same request. Pinned so a future change to the divisor has to face
// the measurement rather than a round number that looked reasonable.
func TestEstimateRequestTokensDoesNotUndercountMeasuredTraffic(t *testing.T) {
	t.Parallel()
	samples := []struct {
		bodyBytes int64
		actual    int
	}{
		{47513, 11136},  // first request: system prompt and tool definitions
		{150818, 50684}, // source code entering the conversation
		{213462, 75302},
		{319659, 116592},
		{433693, 159629},
		{468940, 172922}, // the densest sample: 2.71 bytes/token
		{605301, 220154},
	}
	for _, s := range samples {
		req := &http.Request{ContentLength: s.bodyBytes}
		est := estimateRequestTokens(req)
		if est < s.actual {
			t.Errorf("%d bytes estimated at %d tokens but was charged %d — under-counting is what causes the 429",
				s.bodyBytes, est, s.actual)
		}
	}
}

// The defect this divisor was corrected for: at 4 bytes per token the limiter
// would have paced nothing through the minute that drew the original
// rejection, and 429'd anyway. 2,005,778 tokens were charged in that minute;
// at the measured 2.75 bytes/token that is about 5.5 MB on the wire, which has
// to estimate above the budget for the limiter to hold anything back.
func TestEstimateCatchesTheRejectedMinute(t *testing.T) {
	t.Parallel()
	const chargedTokens = 2_005_778
	// 2.75 bytes per token, in integer arithmetic so the figure stays exact.
	wireBytes := int64(chargedTokens) * 275 / 100
	est := estimateRequestTokens(&http.Request{ContentLength: wireBytes})

	budget := DefaultsFor("gemini", "gemini-3.8-flash").InputTokensPerMinute
	if est <= budget {
		t.Fatalf("the rejected minute (%d bytes) estimates at %d tokens, inside the %d budget: "+
			"the limiter would pace nothing and the 429 would recur", wireBytes, est, budget)
	}
}
