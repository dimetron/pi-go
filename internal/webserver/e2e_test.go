//go:build e2e

package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestServePairE2E_FullLifecycle starts a real ServerV2, exercises the
// complete serve + pair flow over HTTP, and verifies:
//  1. Unauthenticated requests redirect to /pair
//  2. POST /api/pair creates a pairing code + token
//  3. GET /api/status returns "pending" before approval
//  4. After approval, GET /api/status returns "approved"
//  5. Authenticated requests to / are served (not redirected)
//  6. WebSocket endpoint rejects unauthenticated requests
//  7. Graceful shutdown works
func TestServePairE2E_FullLifecycle(t *testing.T) {
	// Listen on a random free port. Earlier revisions hardcoded :8080, which
	// clashes with anything already bound there (e.g. an IDE on a developer
	// machine); using :0 lets the kernel pick a free port.
	srv := NewServerV2(Config{
		Addr:           "127.0.0.1:0",
		PairingTimeout: 30 * time.Second,
		StaticDir:      t.TempDir(), // empty dir — static files not needed for API tests
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	baseURL := "http://" + srv.Addr()

	// Use a client that does NOT follow redirects automatically.
	noRedirect := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// --- Step 1: unauthenticated GET / should redirect to /pair ---
	resp, err := noRedirect.Get(baseURL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("GET / without token: expected 303, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/pair" {
		t.Errorf("expected redirect to /pair, got %q", loc)
	}

	// --- Step 2: POST /api/pair to create pairing code ---
	pairResp, err := http.Post(baseURL+"/api/pair",
		"application/json",
		strings.NewReader(`{"project":"/tmp/e2e-project"}`))
	if err != nil {
		t.Fatalf("POST /api/pair: %v", err)
	}
	defer pairResp.Body.Close()
	if pairResp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/pair: expected 200, got %d", pairResp.StatusCode)
	}

	body, err := io.ReadAll(pairResp.Body)
	if err != nil {
		t.Fatalf("read /api/pair body: %v", err)
	}
	var pr PairResponse
	if err := json.Unmarshal(body, &pr); err != nil {
		t.Fatalf("decode PairResponse: %v", err)
	}
	if len(pr.Code) != 6 {
		t.Errorf("expected 6-digit code, got %q", pr.Code)
	}
	if pr.QR == "" {
		t.Error("QR data is empty")
	}
	// The endpoint is unauthenticated: the token must not be in this body.
	if strings.Contains(string(body), `"token"`) {
		t.Errorf("unauthenticated /api/pair leaked a token: %s", body)
	}

	// The operator learns the token from the startup banner; a same-package
	// test reads it off the server instead.
	token := activeToken(t, srv)

	// --- Step 3: GET /api/status — should be pending ---
	assertStatus(t, baseURL, token, PairStatusPending)

	// --- Step 4: approve the code (simulates mobile app) ---
	approvedToken, err := srv.PairingManager().Approve(pr.Code)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if approvedToken != token {
		t.Errorf("Approve returned token %q, expected %q", approvedToken, token)
	}

	// --- Step 5: GET /api/status — should be approved ---
	assertStatus(t, baseURL, token, PairStatusApproved)

	// --- Step 6: authenticated GET / should NOT redirect ---
	req, _ := http.NewRequest("GET", baseURL+"/", nil)
	req.AddCookie(&http.Cookie{Name: "pi_token", Value: token})
	resp, err = noRedirect.Do(req)
	if err != nil {
		t.Fatalf("authenticated GET /: %v", err)
	}
	resp.Body.Close()
	// Static dir is empty so we get 404 for the HTML file, but NOT a redirect.
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("authenticated GET / should not redirect to /pair")
	}

	// --- Step 7: WebSocket endpoint rejects unauthenticated ---
	wsResp, err := noRedirect.Get(baseURL + "/ws/test-session?token=bad-token")
	if err != nil {
		t.Fatalf("GET /ws/: %v", err)
	}
	wsResp.Body.Close()
	if wsResp.StatusCode != http.StatusUnauthorized {
		t.Errorf("WS without valid token: expected 401, got %d", wsResp.StatusCode)
	}

	// --- Step 8: WebSocket endpoint rejects missing session ID ---
	wsResp, err = noRedirect.Get(baseURL + "/ws/?token=" + token)
	if err != nil {
		t.Fatalf("GET /ws/ empty session: %v", err)
	}
	wsResp.Body.Close()
	if wsResp.StatusCode != http.StatusBadRequest {
		t.Errorf("WS with empty session ID: expected 400, got %d", wsResp.StatusCode)
	}
}

// TestServePairE2E_PairingExpiry verifies that expired codes are rejected.
func TestServePairE2E_PairingExpiry(t *testing.T) {
	srv := NewServerV2(Config{
		Addr:           "127.0.0.1:0",
		PairingTimeout: 1 * time.Millisecond, // expire immediately
		StaticDir:      t.TempDir(),
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	baseURL := "http://" + srv.Addr()

	// Create pair.
	pairResp, err := http.Post(baseURL+"/api/pair",
		"application/json",
		strings.NewReader(`{"project":"."}`))
	if err != nil {
		t.Fatalf("POST /api/pair: %v", err)
	}
	defer pairResp.Body.Close()

	var pr PairResponse
	json.NewDecoder(pairResp.Body).Decode(&pr)
	token := activeToken(t, srv)

	// Wait for expiry.
	time.Sleep(5 * time.Millisecond)

	// Status should be expired.
	assertStatus(t, baseURL, token, PairStatusExpired)

	// Approve should fail.
	_, err = srv.PairingManager().Approve(pr.Code)
	if err == nil {
		t.Error("expected error approving expired code")
	}
}

// TestServePairE2E_MultiplePairs verifies independent pairing codes.
func TestServePairE2E_MultiplePairs(t *testing.T) {
	srv := NewServerV2(Config{
		Addr:           "127.0.0.1:0",
		PairingTimeout: 30 * time.Second,
		StaticDir:      t.TempDir(),
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	baseURL := "http://" + srv.Addr()

	// Create the first pair and approve it. The server caches the "active
	// pair" while it's pending, so a second POST /api/pair would otherwise
	// return the same code/token instead of a fresh pair.
	pr1, token1 := createPair(t, srv, baseURL, "/tmp/project-a")
	if _, err := srv.PairingManager().Approve(pr1.Code); err != nil {
		t.Fatalf("Approve pr1: %v", err)
	}

	// Now create a second, independent pair.
	pr2, token2 := createPair(t, srv, baseURL, "/tmp/project-b")

	if pr1.Code == pr2.Code {
		t.Error("codes should be unique")
	}
	if token1 == token2 {
		t.Error("tokens should be unique")
	}

	assertStatus(t, baseURL, token1, PairStatusApproved)
	assertStatus(t, baseURL, token2, PairStatusPending)

	// Verify project isolation.
	proj, err := srv.PairingManager().GetProject(token1)
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if proj != "/tmp/project-a" {
		t.Errorf("expected /tmp/project-a, got %q", proj)
	}
}

// TestServePairE2E_PairRedirectAfterApproval verifies that GET /pair?token=...
// redirects to / after approval and sets the cookie.
func TestServePairE2E_PairRedirectAfterApproval(t *testing.T) {
	srv := NewServerV2(Config{
		Addr:           "127.0.0.1:0",
		PairingTimeout: 30 * time.Second,
		StaticDir:      t.TempDir(),
	})

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	baseURL := "http://" + srv.Addr()
	noRedirect := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Create and approve a pair.
	pr, token := createPair(t, srv, baseURL, "/tmp/redir-test")
	srv.PairingManager().Approve(pr.Code)

	// GET /pair?token=<approved> should redirect to / with cookie.
	resp, err := noRedirect.Get(baseURL + "/pair?token=" + token)
	if err != nil {
		t.Fatalf("GET /pair?token: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Errorf("expected 303 redirect, got %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("expected redirect to /, got %q", loc)
	}

	// Verify cookie was set.
	var foundCookie bool
	for _, c := range resp.Cookies() {
		if c.Name == "pi_token" && c.Value == token {
			foundCookie = true
			break
		}
	}
	if !foundCookie {
		t.Error("expected pi_token cookie in redirect response")
	}
}

// --- helpers ---

// createPair posts /api/pair and returns the public response along with the
// token the server kept to itself.
func createPair(t *testing.T, srv *ServerV2, baseURL, project string) (PairResponse, string) {
	t.Helper()
	body := fmt.Sprintf(`{"project":%q}`, project)
	resp, err := http.Post(baseURL+"/api/pair", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/pair: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/pair: expected 200, got %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /api/pair body: %v", err)
	}
	if strings.Contains(string(raw), `"token"`) {
		t.Fatalf("unauthenticated /api/pair leaked a token: %s", raw)
	}
	var pr PairResponse
	if err := json.Unmarshal(raw, &pr); err != nil {
		t.Fatalf("decode PairResponse: %v", err)
	}
	return pr, activeToken(t, srv)
}

// activeToken reads the token the server holds for the pair currently on
// offer. It never travels over HTTP any more, so the test takes the same route
// the operator does — straight from the server.
func activeToken(t *testing.T, srv *ServerV2) string {
	t.Helper()
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.activePairToken == "" {
		t.Fatal("server holds no active pair token")
	}
	return srv.activePairToken
}

func assertStatus(t *testing.T, baseURL, token string, expected PairStatus) {
	t.Helper()
	resp, err := http.Get(baseURL + "/api/status?token=" + token)
	if err != nil {
		t.Fatalf("GET /api/status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/status: expected 200, got %d", resp.StatusCode)
	}
	var sr StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		t.Fatalf("decode StatusResponse: %v", err)
	}
	if sr.Status != expected {
		t.Errorf("expected status %q, got %q", expected, sr.Status)
	}
}
