package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDropBefore(t *testing.T) {
	t.Parallel()
	now := time.Now()
	times := []time.Time{now.Add(-3 * time.Minute), now.Add(-90 * time.Second), now.Add(-10 * time.Second), now}
	tests := []struct {
		name   string
		cutoff time.Time
		want   int
	}{
		{"nothing expired", now.Add(-4 * time.Minute), 4},
		{"everything expired", now.Add(time.Second), 0},
		{"drops the two oldest", now.Add(-time.Minute), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := dropBefore(times, tt.cutoff); len(got) != tt.want {
				t.Errorf("dropBefore(%v) len = %d, want %d", tt.cutoff, len(got), tt.want)
			}
		})
	}
}

func TestSumTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		events []tokenSample
		want   int
	}{
		{"empty", nil, 0},
		{"one", []tokenSample{{tokens: 100}}, 100},
		{"several", []tokenSample{{tokens: 100}, {tokens: 250}, {tokens: 1}}, 351},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sumTokens(tt.events); got != tt.want {
				t.Errorf("sumTokens = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestScopeMetricsRecordAndSnapshot(t *testing.T) {
	t.Parallel()
	m := &scopeMetrics{
		provider: "gemini",
		model:    "gemini-3.8-flash",
		limits:   Limits{RequestsPerMinute: 1000, InputTokensPerMinute: 2_000_000},
	}

	m.record(500_000)
	m.record(600_000)
	m.record(700_000)

	got := m.snapshot()
	want := ScopeMetrics{
		Provider: "gemini",
		Model:    "gemini-3.8-flash",

		RequestsPerMinute:     3,
		PeakRequestsPerMinute: 3,
		RequestsLimit:         1000,

		InputTokensPerMinute:     1_800_000,
		PeakInputTokensPerMinute: 1_800_000,
		InputTokensLimit:         2_000_000,
	}
	if got != want {
		t.Fatalf("snapshot = %+v, want %+v", got, want)
	}
}

// The dashboard being mirrored is a peak view: once the window rolls forward
// and the current value drops, the peak observed earlier must still be
// reported, not overwritten by the lower current reading.
func TestScopeMetricsPeakSurvivesWindowExpiry(t *testing.T) {
	t.Parallel()
	m := &scopeMetrics{limits: Limits{InputTokensPerMinute: 2_000_000}}

	m.record(2_000_000)
	if peak := m.snapshot().PeakInputTokensPerMinute; peak != 2_000_000 {
		t.Fatalf("peak after first burst = %d, want 2_000_000", peak)
	}

	// Age the recorded event past the window without a real sleep, the same
	// way ratelimit_test.go backdates spend entries: grab the lock and rewrite
	// the timestamp.
	m.mu.Lock()
	for i := range m.tokenEvents {
		m.tokenEvents[i].at = time.Now().Add(-2 * time.Minute)
	}
	for i := range m.requestTimes {
		m.requestTimes[i] = time.Now().Add(-2 * time.Minute)
	}
	m.mu.Unlock()

	got := m.snapshot()
	if got.InputTokensPerMinute != 0 {
		t.Fatalf("current tokens after expiry = %d, want 0", got.InputTokensPerMinute)
	}
	if got.PeakInputTokensPerMinute != 2_000_000 {
		t.Fatalf("peak after expiry = %d, want 2_000_000 (peaks must not decay)", got.PeakInputTokensPerMinute)
	}
	if got.RequestsPerMinute != 0 {
		t.Fatalf("current requests after expiry = %d, want 0", got.RequestsPerMinute)
	}
	if got.PeakRequestsPerMinute != 1 {
		t.Fatalf("peak requests after expiry = %d, want 1", got.PeakRequestsPerMinute)
	}
}

// A request with no measurable body (chunked, ContentLength unknown) charges
// no tokens but must still count toward the request-rate metric.
func TestScopeMetricsZeroTokenRequestStillCountsAsRequest(t *testing.T) {
	t.Parallel()
	m := &scopeMetrics{}
	m.record(0)
	got := m.snapshot()
	if got.RequestsPerMinute != 1 {
		t.Fatalf("RequestsPerMinute = %d, want 1", got.RequestsPerMinute)
	}
	if got.InputTokensPerMinute != 0 {
		t.Fatalf("InputTokensPerMinute = %d, want 0", got.InputTokensPerMinute)
	}
}

func TestRecordAdmittedAndMetricsSnapshot(t *testing.T) {
	resetMetrics()
	t.Cleanup(resetMetrics)

	recordAdmitted("gemini|gemini-3.8-flash|", "gemini", "gemini-3.8-flash",
		Limits{InputTokensPerMinute: 2_000_000}, 900_000)
	recordAdmitted("gemini|gemini-3.8-flash|", "gemini", "gemini-3.8-flash",
		Limits{InputTokensPerMinute: 2_000_000}, 1_200_000)
	recordAdmitted("openai|gpt-5.2|", "openai", "gpt-5.2",
		Limits{RequestsPerMinute: 500}, 100)

	got := MetricsSnapshot()
	if len(got) != 2 {
		t.Fatalf("MetricsSnapshot returned %d scopes, want 2", len(got))
	}
	// Sorted by provider then model: gemini before openai.
	if got[0].Provider != "gemini" || got[0].Model != "gemini-3.8-flash" {
		t.Fatalf("got[0] = %+v, want gemini/gemini-3.8-flash first", got[0])
	}
	if got[0].InputTokensPerMinute != 2_100_000 {
		t.Fatalf("gemini InputTokensPerMinute = %d, want 2_100_000", got[0].InputTokensPerMinute)
	}
	if got[0].RequestsPerMinute != 2 {
		t.Fatalf("gemini RequestsPerMinute = %d, want 2", got[0].RequestsPerMinute)
	}
	if got[1].Provider != "openai" || got[1].RequestsLimit != 500 {
		t.Fatalf("got[1] = %+v, want openai/gpt-5.2 with RequestsLimit 500", got[1])
	}
}

// The first caller's limits win, mirroring Shared's "first caller wins" rule
// for the Limiter itself: every client on one scope shares one budget, so a
// later, differently-configured caller must not silently rewrite it.
func TestRecordAdmittedFirstLimitsWin(t *testing.T) {
	resetMetrics()
	t.Cleanup(resetMetrics)

	recordAdmitted("scope", "p", "m", Limits{InputTokensPerMinute: 111}, 1)
	recordAdmitted("scope", "p", "m", Limits{InputTokensPerMinute: 999}, 1)

	got := MetricsSnapshot()
	if len(got) != 1 || got[0].InputTokensLimit != 111 {
		t.Fatalf("MetricsSnapshot = %+v, want a single scope with InputTokensLimit 111", got)
	}
}

func TestWriteMetricsFormat(t *testing.T) {
	t.Parallel()
	scopes := []ScopeMetrics{
		{
			Provider: "gemini", Model: "gemini-3.8-flash",
			RequestsPerMinute: 12, PeakRequestsPerMinute: 40, RequestsLimit: 1000,
			InputTokensPerMinute: 1_900_000, PeakInputTokensPerMinute: 2_005_778, InputTokensLimit: 2_000_000,
		},
	}
	var sb strings.Builder
	if err := WriteMetrics(&sb, scopes); err != nil {
		t.Fatalf("WriteMetrics: %v", err)
	}
	out := sb.String()

	for _, want := range []string{
		"# TYPE pi_ratelimit_requests_per_minute gauge",
		`pi_ratelimit_requests_per_minute{provider="gemini",model="gemini-3.8-flash"} 12`,
		`pi_ratelimit_requests_per_minute_peak{provider="gemini",model="gemini-3.8-flash"} 40`,
		`pi_ratelimit_requests_per_minute_limit{provider="gemini",model="gemini-3.8-flash"} 1000`,
		`pi_ratelimit_input_tokens_per_minute{provider="gemini",model="gemini-3.8-flash"} 1900000`,
		`pi_ratelimit_input_tokens_per_minute_peak{provider="gemini",model="gemini-3.8-flash"} 2005778`,
		`pi_ratelimit_input_tokens_per_minute_limit{provider="gemini",model="gemini-3.8-flash"} 2000000`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\ngot:\n%s", want, out)
		}
	}
}

// Label values are quoted the way the Prometheus text format requires, so a
// provider or model name containing a quote cannot break the exposition.
func TestWriteMetricsEscapesLabels(t *testing.T) {
	t.Parallel()
	scopes := []ScopeMetrics{{Provider: `weird"provider`, Model: "m"}}
	var sb strings.Builder
	if err := WriteMetrics(&sb, scopes); err != nil {
		t.Fatalf("WriteMetrics: %v", err)
	}
	if !strings.Contains(sb.String(), `provider="weird\"provider"`) {
		t.Fatalf("output does not escape the embedded quote:\n%s", sb.String())
	}
}

// httptest.NewRecorder is safe here (unlike httptest.NewServer): it fakes an
// http.ResponseWriter without binding a local listener, so it does not hit
// the sandbox trap documented in CLAUDE.md.
func TestMetricsHandlerServesExpositionFormat(t *testing.T) {
	resetMetrics()
	t.Cleanup(resetMetrics)
	recordAdmitted("gemini|gemini-3.8-flash|", "gemini", "gemini-3.8-flash",
		Limits{InputTokensPerMinute: 2_000_000}, 500_000)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	MetricsHandler().ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain prefix", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `pi_ratelimit_input_tokens_per_minute{provider="gemini",model="gemini-3.8-flash"} 500000`) {
		t.Fatalf("handler body missing recorded scope:\n%s", body)
	}
}
