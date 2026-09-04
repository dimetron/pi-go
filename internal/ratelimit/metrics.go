package ratelimit

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

// This file mirrors, for observability, the two per-minute figures Google AI
// Studio's quota dashboard shows for a Gemini model: "Peak requests per
// minute" and "Peak input tokens per minute". It exists so a user can see
// locally, while a turn is running, what the provider console only shows
// after the fact.
//
// It deliberately keeps its own rolling-window ledger rather than reading
// Limiter's internal admission state. Limiter's own bookkeeping for the token
// budget has changed shape before (a token bucket, then a rolling window; see
// fix/ratelimit-rolling-window) and the request budget has never had a ledger
// at all — RequestsPerMinute is paced with a plain token-bucket rate.Limiter,
// which has nothing to report a rolling *count* from. A metrics path wired to
// either representation would have to change every time admission logic does,
// or would have no ledger to read at all. Recording independently, at the one
// point every provider's requests already pass through (Transport.RoundTrip,
// right after Wait grants admission), gets the same numbers the provider
// counts without coupling to how admission happens to be implemented.

// metricsWindow is the width of the rolling window these metrics are computed
// over. It matches window in ratelimit.go: both describe the same per-minute
// quota the provider enforces, so a scraper's numbers describe the same 60
// seconds a 429 would name.
const metricsWindow = time.Minute

// tokenSample is one admitted request's token charge, timestamped so it can
// be dropped once it falls out of the rolling window.
type tokenSample struct {
	at     time.Time
	tokens int
}

// scopeMetrics accumulates the rolling-window request and token counters for
// one provider+model scope (see ScopeFor), plus the high-water mark observed
// for each since the process started.
type scopeMetrics struct {
	provider, model string
	limits          Limits

	mu           sync.Mutex
	requestTimes []time.Time
	peakRequests int
	tokenEvents  []tokenSample
	peakTokens   int
}

// record admits one request charging inputTokens into the rolling window and
// updates the peak counters if this admission set a new high.
//
// The peak only needs checking on insertion: the rolling sum (or count) never
// increases except when a new event is appended, so its maximum over time is
// always reached immediately after an insertion, never after an expiry.
func (m *scopeMetrics) record(inputTokens int) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requestTimes = append(m.requestTimes, now)
	m.requestTimes = dropBefore(m.requestTimes, now.Add(-metricsWindow))
	m.peakRequests = max(m.peakRequests, len(m.requestTimes))

	if inputTokens > 0 {
		m.tokenEvents = append(m.tokenEvents, tokenSample{at: now, tokens: inputTokens})
	}
	m.tokenEvents = dropExpiredTokens(m.tokenEvents, now.Add(-metricsWindow))
	m.peakTokens = max(m.peakTokens, sumTokens(m.tokenEvents))
}

// snapshot reports the current rolling-window values and the peaks observed
// so far. It prunes entries that have fallen out of the window since the last
// record, so a scrape between requests still reads the true current value
// rather than a stale one left over from the last admission.
func (m *scopeMetrics) snapshot() ScopeMetrics {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requestTimes = dropBefore(m.requestTimes, now.Add(-metricsWindow))
	m.tokenEvents = dropExpiredTokens(m.tokenEvents, now.Add(-metricsWindow))

	return ScopeMetrics{
		Provider: m.provider,
		Model:    m.model,

		RequestsPerMinute:     len(m.requestTimes),
		PeakRequestsPerMinute: m.peakRequests,
		RequestsLimit:         m.limits.RequestsPerMinute,

		InputTokensPerMinute:     sumTokens(m.tokenEvents),
		PeakInputTokensPerMinute: m.peakTokens,
		InputTokensLimit:         m.limits.InputTokensPerMinute,
	}
}

// dropBefore drops the leading run of times older than cutoff. requestTimes
// is append-only and therefore already sorted, so a linear scan from the
// front is sufficient — no need to search or resort.
func dropBefore(times []time.Time, cutoff time.Time) []time.Time {
	keep := 0
	for keep < len(times) && times[keep].Before(cutoff) {
		keep++
	}
	return times[keep:]
}

// dropExpiredTokens is dropBefore's counterpart for tokenSample, which is
// also append-only and therefore already ordered by at.
func dropExpiredTokens(events []tokenSample, cutoff time.Time) []tokenSample {
	keep := 0
	for keep < len(events) && events[keep].at.Before(cutoff) {
		keep++
	}
	return events[keep:]
}

func sumTokens(events []tokenSample) int {
	sum := 0
	for _, e := range events {
		sum += e.tokens
	}
	return sum
}

// ScopeMetrics is a rendered snapshot of one provider+model scope's
// rate-limit metrics: the current rolling-60s value and the peak observed
// since the process started, for both requests and input tokens, alongside
// the configured budget each is paced against.
type ScopeMetrics struct {
	Provider string
	Model    string

	// RequestsPerMinute is the request count admitted in the trailing 60s.
	RequestsPerMinute int
	// PeakRequestsPerMinute is the highest RequestsPerMinute has been since
	// the process started.
	PeakRequestsPerMinute int
	// RequestsLimit is the configured requests-per-minute budget. Zero means
	// unlimited.
	RequestsLimit int

	// InputTokensPerMinute is the input tokens admitted in the trailing 60s.
	InputTokensPerMinute int
	// PeakInputTokensPerMinute is the highest InputTokensPerMinute has been
	// since the process started.
	PeakInputTokensPerMinute int
	// InputTokensLimit is the configured input-tokens-per-minute budget. Zero
	// means unlimited.
	InputTokensLimit int
}

var (
	metricsMu      sync.Mutex
	metricsByScope = map[string]*scopeMetrics{}
)

// recordAdmitted records one admitted request against scope's rolling-window
// metrics, creating the scope's counters on first use.
//
// Like Shared, the first caller for a scope wins: if a later Transport for
// the same scope somehow disagreed on limits, the metrics would still be
// read against the budget the scope was first observed with, matching the
// one Limiter every caller on that scope actually shares.
func recordAdmitted(scope, provider, model string, limits Limits, inputTokens int) {
	metricsMu.Lock()
	m, ok := metricsByScope[scope]
	if !ok {
		m = &scopeMetrics{provider: provider, model: model, limits: limits}
		metricsByScope[scope] = m
	}
	metricsMu.Unlock()
	m.record(inputTokens)
}

// MetricsSnapshot returns the current rolling-window and peak values for
// every provider+model scope that has admitted at least one request, sorted
// by provider then model for stable output.
func MetricsSnapshot() []ScopeMetrics {
	metricsMu.Lock()
	scopes := make([]*scopeMetrics, 0, len(metricsByScope))
	for _, m := range metricsByScope {
		scopes = append(scopes, m)
	}
	metricsMu.Unlock()

	out := make([]ScopeMetrics, len(scopes))
	for i, m := range scopes {
		out[i] = m.snapshot()
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Model < out[j].Model
	})
	return out
}

// resetMetrics drops every recorded scope. Tests only: metricsByScope is
// process-wide by design, which would otherwise leak state between cases.
func resetMetrics() {
	metricsMu.Lock()
	defer metricsMu.Unlock()
	metricsByScope = map[string]*scopeMetrics{}
}

// Prometheus gauge names this package exposes. All six carry the
// {provider="...",model="..."} label pair; none carry the base URL — the
// dashboard being mirrored groups by model, not by endpoint, and a base URL
// can embed a hostname or path a user would not want in scraped metric
// labels.
const (
	metricRequests      = "pi_ratelimit_requests_per_minute"
	metricRequestsPeak  = "pi_ratelimit_requests_per_minute_peak"
	metricRequestsLimit = "pi_ratelimit_requests_per_minute_limit"
	metricTokens        = "pi_ratelimit_input_tokens_per_minute"
	metricTokensPeak    = "pi_ratelimit_input_tokens_per_minute_peak"
	metricTokensLimit   = "pi_ratelimit_input_tokens_per_minute_limit"
)

// gauge is one Prometheus gauge family this package exposes: a name, its HELP
// text, and how to read its value out of a ScopeMetrics.
type gauge struct {
	name string
	help string
	val  func(ScopeMetrics) int
}

// gauges lists every metric WriteMetrics renders, in the order they appear in
// the exposition. Order does not matter to Prometheus, but a stable order
// makes curl output and diffs readable.
var gauges = []gauge{
	{metricRequests, "Requests admitted in the trailing 60s, per provider+model.",
		func(s ScopeMetrics) int { return s.RequestsPerMinute }},
	{metricRequestsPeak, "Peak requests admitted in any trailing 60s window, per provider+model, since process start.",
		func(s ScopeMetrics) int { return s.PeakRequestsPerMinute }},
	{metricRequestsLimit, "Configured requests-per-minute budget, per provider+model. 0 means unlimited.",
		func(s ScopeMetrics) int { return s.RequestsLimit }},
	{metricTokens, "Input tokens admitted in the trailing 60s, per provider+model.",
		func(s ScopeMetrics) int { return s.InputTokensPerMinute }},
	{metricTokensPeak, "Peak input tokens admitted in any trailing 60s window, per provider+model, since process start.",
		func(s ScopeMetrics) int { return s.PeakInputTokensPerMinute }},
	{metricTokensLimit, "Configured input-tokens-per-minute budget, per provider+model. 0 means unlimited.",
		func(s ScopeMetrics) int { return s.InputTokensLimit }},
}

// WriteMetrics renders every scope in scopes as Prometheus text exposition
// format (https://prometheus.io/docs/instrumenting/exposition_formats/): six
// gauge families, each with one HELP/TYPE header followed by one sample line
// per scope.
//
// Reporting both the current rolling value and its peak is the point: the
// dashboard being mirrored is a peak view precisely because a point-in-time
// gauge alone hides the bursts that trip a 429 — see the package-level
// comment in ratelimit.go. The limit gauges let a scraper compute headroom
// the same way the dashboard's red line does.
func WriteMetrics(w io.Writer, scopes []ScopeMetrics) error {
	for _, g := range gauges {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", g.name, g.help, g.name); err != nil {
			return err
		}
		for _, s := range scopes {
			_, err := fmt.Fprintf(w, "%s{provider=%s,model=%s} %d\n",
				g.name, strconv.Quote(s.Provider), strconv.Quote(s.Model), g.val(s))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// MetricsHandler serves every provider+model scope's rate-limit metrics in
// Prometheus text exposition format. Mount it at whatever path the caller
// chooses (e.g. "/metrics"); it does not register itself anywhere.
func MetricsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		// A ResponseWriter write failure means the client is already gone;
		// nothing useful can be done with the error here.
		_ = WriteMetrics(w, MetricsSnapshot())
	})
}
