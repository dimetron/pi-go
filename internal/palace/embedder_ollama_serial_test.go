package palace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ollama/ollama/api"
)

// newTestOllamaEmbedder builds an embedder pointed at srv, bypassing
// NewOllamaEmbedder's reachability probe (which would need a /api/tags route).
func newTestOllamaEmbedder(t *testing.T, srv *httptest.Server) *ollamaEmbedder {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parsing test server URL: %v", err)
	}
	return &ollamaEmbedder{
		client: api.NewClient(u, srv.Client()),
		model:  "test-model",
	}
}

// writeEmbeddings replies with one vector per input, which is what Embed's
// length check requires.
func writeEmbeddings(w http.ResponseWriter, n int) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"embeddings":[`)
	for i := range n {
		if i > 0 {
			fmt.Fprint(w, ",")
		}
		fmt.Fprint(w, `[0.1,0.2,0.3]`)
	}
	fmt.Fprint(w, `]}`)
}

// TestOllamaEmbedSerializesAcrossGoroutines is the regression test for the
// concurrency fix: mining embeds from a worker pool while drawer_service embeds
// on every search, and overlapping requests are what made Ollama restart its
// model runner mid-batch ("connection reset by peer" against the runner port).
//
// The assertion is on observed concurrency at the server, not on the lock:
// testing that two callers never overlap is the actual contract.
func TestOllamaEmbedSerializesAcrossGoroutines(t *testing.T) {
	var inFlight, peak atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := inFlight.Add(1)
		for {
			old := peak.Load()
			if cur <= old || peak.CompareAndSwap(old, cur) {
				break
			}
		}
		// Hold the request open long enough that any real overlap is observed.
		time.Sleep(15 * time.Millisecond)
		inFlight.Add(-1)
		writeEmbeddings(w, 1)
	}))
	defer srv.Close()

	e := newTestOllamaEmbedder(t, srv)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Embed([]string{fmt.Sprintf("text-%d", i)}); err != nil {
				t.Errorf("Embed: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := peak.Load(); got != 1 {
		t.Errorf("peak concurrent requests at the daemon = %d, want 1 "+
			"(Embed must serialize every caller in the process)", got)
	}
}

// TestOllamaEmbedRetriesTransientReset covers the other half of the reported
// failure: even with our own calls serialized, Ollama evicts models on its own
// idle timer, and a batch in flight when that happens dies with a reset
// connection. Without a retry those chunks are stored with nil vectors and go
// silently missing from every later search.
func TestOllamaEmbedRetriesTransientReset(t *testing.T) {
	orig := ollamaEmbedRetryDelay
	ollamaEmbedRetryDelay = time.Millisecond
	defer func() { ollamaEmbedRetryDelay = orig }()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			// Hijack and close without a response: the client sees the
			// connection drop, which is the shape a dying runner produces.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("test server does not support hijacking")
				return
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("hijack: %v", err)
				return
			}
			conn.Close()
			return
		}
		writeEmbeddings(w, 2)
	}))
	defer srv.Close()

	e := newTestOllamaEmbedder(t, srv)

	got, err := e.Embed([]string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed should have recovered on retry, got: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d embeddings, want 2", len(got))
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("server saw %d calls, want 2 (one failure + one retry)", n)
	}
}

// TestOllamaEmbedGivesUpAfterRetries checks the retry is bounded — a daemon
// that is genuinely down must surface an error rather than stall.
func TestOllamaEmbedGivesUpAfterRetries(t *testing.T) {
	orig := ollamaEmbedRetryDelay
	ollamaEmbedRetryDelay = time.Millisecond
	defer func() { ollamaEmbedRetryDelay = orig }()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		conn.Close()
	}))
	defer srv.Close()

	e := newTestOllamaEmbedder(t, srv)

	if _, err := e.Embed([]string{"a"}); err == nil {
		t.Fatal("expected an error once retries are exhausted")
	}

	// The effort is now retries × shrink steps: each batch size gets its own
	// round of retries before halving, and halving stops at the floor. Bound it
	// rather than pin an exact count, which is an implementation detail — the
	// contract is that it terminates and does not retry forever.
	steps := 1
	for b := ollamaEmbedBatchSize; b > ollamaMinEmbedBatch; b = max(b/2, ollamaMinEmbedBatch) {
		steps++
	}
	if n := int(calls.Load()); n > ollamaEmbedRetries*steps {
		t.Errorf("server saw %d calls, want at most %d (retries × shrink steps)",
			n, ollamaEmbedRetries*steps)
	}
	if e.batch != ollamaMinEmbedBatch {
		t.Errorf("batch = %d, want the floor %d", e.batch, ollamaMinEmbedBatch)
	}
}

// TestOllamaEmbedShrinksBatchOnRepeatedFailure covers the observed failure on a
// real 21k-chunk run: embeddinggemma's runner reset the connection partway
// through tokenizing a 512-text payload, every retry of that same payload hit
// the same wall, and the batch was then dropped — storing 512 chunks with nil
// vectors that no later search would ever match.
//
// The size is the cause, so the batch has to adapt. This server refuses
// anything larger than 128 and succeeds below it.
func TestOllamaEmbedShrinksBatchOnRepeatedFailure(t *testing.T) {
	orig := ollamaEmbedRetryDelay
	ollamaEmbedRetryDelay = time.Millisecond
	defer func() { ollamaEmbedRetryDelay = orig }()

	const limit = 128
	var maxAccepted atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
			return
		}
		if len(req.Input) > limit {
			// Drop the connection the way an overloaded runner does.
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					conn.Close()
				}
			}
			return
		}
		if n := int64(len(req.Input)); n > maxAccepted.Load() {
			maxAccepted.Store(n)
		}
		writeEmbeddings(w, len(req.Input))
	}))
	defer srv.Close()

	e := newTestOllamaEmbedder(t, srv)

	texts := make([]string, 600)
	for i := range texts {
		texts[i] = fmt.Sprintf("chunk-%d", i)
	}

	got, err := e.Embed(texts)
	if err != nil {
		t.Fatalf("Embed should have adapted to the server's limit, got: %v", err)
	}
	// Every chunk must come back with a vector — the whole point is that none
	// are silently dropped.
	if len(got) != len(texts) {
		t.Errorf("got %d embeddings, want %d", len(got), len(texts))
	}
	if n := maxAccepted.Load(); n > limit {
		t.Errorf("server accepted a batch of %d, above its %d limit", n, limit)
	}
	if e.batch > limit {
		t.Errorf("batch size settled at %d, expected it to shrink to <= %d", e.batch, limit)
	}
	// The reduction must stick, or the next call walks into the same wall.
	if e.batch >= ollamaEmbedBatchSize {
		t.Errorf("batch size %d was not retained after shrinking", e.batch)
	}
}

// TestOllamaEmbedStopsShrinkingAtFloor: a server that refuses everything must
// produce an error, not an infinite halving loop.
func TestOllamaEmbedStopsShrinkingAtFloor(t *testing.T) {
	orig := ollamaEmbedRetryDelay
	ollamaEmbedRetryDelay = time.Millisecond
	defer func() { ollamaEmbedRetryDelay = orig }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				conn.Close()
			}
		}
	}))
	defer srv.Close()

	e := newTestOllamaEmbedder(t, srv)

	done := make(chan error, 1)
	go func() { _, err := e.Embed(make([]string, 600)); done <- err }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error when every batch size fails")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Embed did not terminate: the shrink loop has no floor")
	}
	if e.batch != ollamaMinEmbedBatch {
		t.Errorf("batch = %d, want the floor %d", e.batch, ollamaMinEmbedBatch)
	}
}

func TestIsTransientOllamaErr(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"econnreset", syscall.ECONNRESET, true},
		{"epipe", syscall.EPIPE, true},
		{"eof", io.EOF, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"wrapped reset", fmt.Errorf("post /api/embed: %w", syscall.ECONNRESET), true},
		{"reset by message", errors.New(`Post "http://127.0.0.1:63348/tokenize": read tcp: connection reset by peer`), true},
		{"connection refused", errors.New("dial tcp: connection refused"), true},
		// A request that will fail identically every time must not be retried.
		{"model not found", errors.New(`model "nope" not found, try pulling it first`), false},
		{"bad request", errors.New("invalid input: empty"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransientOllamaErr(tt.err); got != tt.want {
				t.Errorf("isTransientOllamaErr(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
