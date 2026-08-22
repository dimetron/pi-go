package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Device-authorization polling has five terminal states — pending, slow_down,
// expired, denied and success — and one piece of arithmetic, the cumulative
// +5s backoff RFC 8628 §3.5 mandates. The pre-existing TestPollDeviceToken_SlowDown
// proved only that polling continues past a slow_down and then succeeds; it
// never asserted the interval bump. These tests pin both: the classification
// of every server answer, and the backoff arithmetic itself.
//
// No test here waits out a real backoff interval. The arithmetic is pinned
// through slowDownInterval with a recording ticker, and the classification
// through pollDeviceTokenOnce, which performs exactly one request.

// cogAuthProvider builds a provider whose token endpoint is the given URL and
// whose key extraction is the identity on AccessToken.
func cogAuthProvider(tokenURL string) Provider {
	return Provider{
		Name:       "cogtest",
		EnvVar:     "COGTEST_KEY",
		TokenURL:   tokenURL,
		ClientID:   "client",
		TokenToKey: func(tok *TokenResponse) string { return tok.AccessToken },
	}
}

// cogAuthServer serves one JSON body per request, walking the list and
// repeating the last entry once exhausted. status is the HTTP status used for
// bodies carrying an "error" field.
func cogAuthServer(t *testing.T, bodies ...map[string]any) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		body := bodies[min(calls, len(bodies)-1)]
		calls++
		w.Header().Set("Content-Type", "application/json")
		if _, isErr := body["error"]; isErr {
			w.WriteHeader(http.StatusBadRequest)
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// cogAuthTicker records every Reset it is given, standing in for the real
// *time.Ticker so the backoff can be observed without waiting seconds for it.
type cogAuthTicker struct {
	resets []time.Duration
}

func (c *cogAuthTicker) Reset(d time.Duration) { c.resets = append(c.resets, d) }

// TestSlowDownIntervalIsCumulative pins the arithmetic the old test left
// unasserted: each slow_down adds exactly 5 seconds to the interval, the
// ticker is retimed to the new interval every time, and the bumps accumulate
// rather than resetting to a fixed backoff.
func TestSlowDownIntervalIsCumulative(t *testing.T) {
	ticker := &cogAuthTicker{}

	interval := 1
	var seen []int
	for range 4 {
		interval = slowDownInterval(interval, ticker)
		seen = append(seen, interval)
	}

	wantIntervals := []int{6, 11, 16, 21}
	if len(seen) != len(wantIntervals) {
		t.Fatalf("got %d intervals, want %d", len(seen), len(wantIntervals))
	}
	for i, want := range wantIntervals {
		if seen[i] != want {
			t.Errorf("after %d slow_down responses interval = %d, want %d", i+1, seen[i], want)
		}
	}

	wantResets := []time.Duration{6 * time.Second, 11 * time.Second, 16 * time.Second, 21 * time.Second}
	if len(ticker.resets) != len(wantResets) {
		t.Fatalf("ticker was reset %d times, want %d", len(ticker.resets), len(wantResets))
	}
	for i, want := range wantResets {
		if ticker.resets[i] != want {
			t.Errorf("reset %d = %v, want %v", i+1, ticker.resets[i], want)
		}
	}
}

// TestSlowDownIntervalFromDefault pins the bump against the default interval,
// the value deviceTokenInterval supplies when the server names none.
func TestSlowDownIntervalFromDefault(t *testing.T) {
	ticker := &cogAuthTicker{}
	if got := slowDownInterval(5, ticker); got != 10 {
		t.Errorf("slowDownInterval(5) = %d, want 10", got)
	}
	if len(ticker.resets) != 1 || ticker.resets[0] != 10*time.Second {
		t.Errorf("resets = %v, want one reset of 10s", ticker.resets)
	}
}

// TestSlowDownIntervalAcceptsARealTicker pins that the seam is the real
// ticker's own contract, not a test-only shape.
func TestSlowDownIntervalAcceptsARealTicker(t *testing.T) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	if got := slowDownInterval(1, ticker); got != 6 {
		t.Errorf("slowDownInterval(1) = %d, want 6", got)
	}
}

func TestDeviceTokenInterval(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{"a negative interval falls back to the RFC default", -3, 5},
		{"a zero interval falls back to the RFC default", 0, 5},
		{"one second is honored", 1, 1},
		{"the server's interval is honored", 7, 7},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deviceTokenInterval(&DeviceCodeResponse{Interval: tt.in}); got != tt.want {
				t.Errorf("deviceTokenInterval(%d) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestDeviceTokenDeadline(t *testing.T) {
	t.Run("a zero expires_in means no deadline", func(t *testing.T) {
		if got := deviceTokenDeadline(&DeviceCodeResponse{ExpiresIn: 0}); !got.IsZero() {
			t.Errorf("deadline = %v, want the zero time", got)
		}
	})
	t.Run("a negative expires_in means no deadline", func(t *testing.T) {
		if got := deviceTokenDeadline(&DeviceCodeResponse{ExpiresIn: -1}); !got.IsZero() {
			t.Errorf("deadline = %v, want the zero time", got)
		}
	})
	t.Run("a positive expires_in lands that many seconds ahead", func(t *testing.T) {
		before := time.Now()
		got := deviceTokenDeadline(&DeviceCodeResponse{ExpiresIn: 600})
		if got.Before(before.Add(599*time.Second)) || got.After(time.Now().Add(601*time.Second)) {
			t.Errorf("deadline = %v, want roughly 600s after %v", got, before)
		}
	})
}

// TestPollDeviceTokenOnceStates pins each answer a device endpoint can give to
// one poll: which ones end the flow, which ones continue it, and which one
// asks for a longer interval.
func TestPollDeviceTokenOnceStates(t *testing.T) {
	tests := []struct {
		name         string
		body         map[string]any
		wantContinue bool // nil Result: keep polling
		wantSlowDown bool
		wantErrPart  string // substring of Result.Err, "" when the poll succeeded
		wantKey      string
	}{
		{
			name:         "authorization_pending keeps polling at the same interval",
			body:         map[string]any{"error": "authorization_pending"},
			wantContinue: true,
		},
		{
			name:         "slow_down keeps polling and asks for a longer interval",
			body:         map[string]any{"error": "slow_down"},
			wantContinue: true,
			wantSlowDown: true,
		},
		{
			name:        "access_denied ends the flow with the server's reason",
			body:        map[string]any{"error": "access_denied"},
			wantErrPart: "access_denied",
		},
		{
			name:        "expired_token ends the flow with the server's reason",
			body:        map[string]any{"error": "expired_token"},
			wantErrPart: "expired_token",
		},
		{
			name:    "a token response ends the flow with the key",
			body:    map[string]any{"access_token": "tok-123"},
			wantKey: "tok-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, calls := cogAuthServer(t, tt.body)
			res, slowDown := pollDeviceTokenOnce(context.Background(), cogAuthProvider(srv.URL), &DeviceCodeResponse{DeviceCode: "dc"}, time.Time{})

			if *calls != 1 {
				t.Errorf("made %d requests, want exactly 1", *calls)
			}
			if slowDown != tt.wantSlowDown {
				t.Errorf("slowDown = %v, want %v", slowDown, tt.wantSlowDown)
			}
			if tt.wantContinue {
				if res != nil {
					t.Fatalf("Result = %+v, want nil so polling continues", res)
				}
				return
			}
			if res == nil {
				t.Fatal("Result = nil, want a terminal result")
			}
			if res.Provider != "cogtest" {
				t.Errorf("Provider = %q, want %q", res.Provider, "cogtest")
			}
			if tt.wantErrPart != "" {
				if res.Err == nil || !strings.Contains(res.Err.Error(), tt.wantErrPart) {
					t.Fatalf("Err = %v, want it to mention %q", res.Err, tt.wantErrPart)
				}
				return
			}
			if res.Err != nil {
				t.Fatalf("Err = %v, want nil", res.Err)
			}
			if res.APIKey != tt.wantKey {
				t.Errorf("APIKey = %q, want %q", res.APIKey, tt.wantKey)
			}
			if res.EnvVar != "COGTEST_KEY" {
				t.Errorf("EnvVar = %q, want %q", res.EnvVar, "COGTEST_KEY")
			}
		})
	}
}

// TestPollDeviceTokenOnceExpiredDeadline pins that a passed deadline is
// checked before the request goes out — an expired code must not be polled
// again.
func TestPollDeviceTokenOnceExpiredDeadline(t *testing.T) {
	srv, calls := cogAuthServer(t, map[string]any{"access_token": "never-read"})

	res, slowDown := pollDeviceTokenOnce(
		context.Background(),
		cogAuthProvider(srv.URL),
		&DeviceCodeResponse{DeviceCode: "dc"},
		time.Now().Add(-time.Second),
	)

	if *calls != 0 {
		t.Errorf("made %d requests past the deadline, want none", *calls)
	}
	if slowDown {
		t.Error("slowDown = true, want false")
	}
	if res == nil || res.Err == nil {
		t.Fatalf("Result = %+v, want a terminal expiry result", res)
	}
	if want := "device authorization timed out: device code expired"; res.Err.Error() != want {
		t.Errorf("Err = %q, want %q", res.Err, want)
	}
	if res.APIKey != "" {
		t.Errorf("APIKey = %q, want empty", res.APIKey)
	}
}

// TestPollDeviceTokenOnceFutureDeadline pins the other side of the deadline
// check: a deadline still ahead lets the request through.
func TestPollDeviceTokenOnceFutureDeadline(t *testing.T) {
	srv, calls := cogAuthServer(t, map[string]any{"access_token": "tok"})

	res, _ := pollDeviceTokenOnce(
		context.Background(),
		cogAuthProvider(srv.URL),
		&DeviceCodeResponse{DeviceCode: "dc"},
		time.Now().Add(time.Hour),
	)

	if *calls != 1 {
		t.Errorf("made %d requests, want 1", *calls)
	}
	if res == nil || res.APIKey != "tok" {
		t.Fatalf("Result = %+v, want the token", res)
	}
}

// TestPollDeviceTokenOnceTransportError pins that a request that never reaches
// a server ends the flow rather than looping forever.
func TestPollDeviceTokenOnceTransportError(t *testing.T) {
	res, slowDown := pollDeviceTokenOnce(
		context.Background(),
		cogAuthProvider("http://127.0.0.1:1/token"),
		&DeviceCodeResponse{DeviceCode: "dc"},
		time.Time{},
	)
	if slowDown {
		t.Error("slowDown = true, want false")
	}
	if res == nil || res.Err == nil {
		t.Fatalf("Result = %+v, want a terminal error result", res)
	}
}

// TestPollDeviceTokenTerminalStates drives the whole loop end to end, at a
// one-second interval, so the states are pinned through the exported entry
// point as well as through the helper. These cases run unchanged against the
// pre-refactor implementation.
func TestPollDeviceTokenTerminalStates(t *testing.T) {
	t.Run("a nil device response is a caller error, not a Result", func(t *testing.T) {
		res, err := PollDeviceToken(context.Background(), Provider{Name: "cogtest"}, nil)
		if res != nil {
			t.Errorf("Result = %+v, want nil", res)
		}
		if err == nil || err.Error() != "nil device code response" {
			t.Fatalf("err = %v, want \"nil device code response\"", err)
		}
	})

	t.Run("pending then a token yields the key", func(t *testing.T) {
		srv, calls := cogAuthServer(t,
			map[string]any{"error": "authorization_pending"},
			map[string]any{"access_token": "tok-late"},
		)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		res, err := PollDeviceToken(ctx, cogAuthProvider(srv.URL), &DeviceCodeResponse{DeviceCode: "dc", Interval: 1})
		if err != nil {
			t.Fatalf("PollDeviceToken: %v", err)
		}
		if res.Err != nil {
			t.Fatalf("Result.Err = %v, want nil", res.Err)
		}
		if res.APIKey != "tok-late" {
			t.Errorf("APIKey = %q, want %q", res.APIKey, "tok-late")
		}
		if *calls != 2 {
			t.Errorf("made %d requests, want 2 (one pending, one success)", *calls)
		}
	})

	t.Run("a denial ends the poll on the first tick", func(t *testing.T) {
		srv, calls := cogAuthServer(t, map[string]any{"error": "access_denied"})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		res, err := PollDeviceToken(ctx, cogAuthProvider(srv.URL), &DeviceCodeResponse{DeviceCode: "dc", Interval: 1})
		if err != nil {
			t.Fatalf("PollDeviceToken: %v", err)
		}
		if res.Err == nil || !strings.Contains(res.Err.Error(), "access_denied") {
			t.Fatalf("Result.Err = %v, want it to mention access_denied", res.Err)
		}
		if *calls != 1 {
			t.Errorf("made %d requests, want 1", *calls)
		}
	})

	t.Run("a canceled context reports the context error", func(t *testing.T) {
		srv, _ := cogAuthServer(t, map[string]any{"error": "authorization_pending"})
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		res, err := PollDeviceToken(ctx, cogAuthProvider(srv.URL), &DeviceCodeResponse{DeviceCode: "dc", Interval: 30})
		if err != nil {
			t.Fatalf("PollDeviceToken: %v", err)
		}
		if res.Err == nil || !strings.Contains(res.Err.Error(), "context deadline exceeded") {
			t.Fatalf("Result.Err = %v, want the context deadline error", res.Err)
		}
		if res.Provider != "cogtest" {
			t.Errorf("Provider = %q, want %q", res.Provider, "cogtest")
		}
	})
}
