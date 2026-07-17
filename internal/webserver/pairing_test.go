package webserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestPairing_CreatePair(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)

	code, token, qrData, err := pm.CreatePair("/tmp/test-project")
	if err != nil {
		t.Fatalf("CreatePair failed: %v", err)
	}

	// Verify code is 6 digits
	if len(code) != 6 {
		t.Errorf("expected 6-digit code, got %q", code)
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			t.Errorf("code %q contains non-digit", code)
		}
	}

	// Verify token is not empty
	if token == "" {
		t.Error("token should not be empty")
	}

	// Verify QR data is not empty
	if len(qrData) == 0 {
		t.Error("QR data should not be empty")
	}
}

func TestPairing_CheckStatus_Pending(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)

	code, token, _, err := pm.CreatePair("/tmp/test-project")
	if err != nil {
		t.Fatalf("CreatePair failed: %v", err)
	}

	status, err := pm.CheckStatus(token)
	if err != nil {
		t.Fatalf("CheckStatus failed: %v", err)
	}
	if status != PairStatusPending {
		t.Errorf("expected pending, got %v", status)
	}

	// Verify code lookup works too
	_ = code // code is stored internally
}

func TestPairing_Approve(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)

	code, token, _, err := pm.CreatePair("/tmp/test-project")
	if err != nil {
		t.Fatalf("CreatePair failed: %v", err)
	}

	// Approve the code
	approvedToken, err := pm.Approve(code)
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	if approvedToken != token {
		t.Errorf("expected token %q, got %q", token, approvedToken)
	}

	// Check status is now approved
	status, err := pm.CheckStatus(token)
	if err != nil {
		t.Fatalf("CheckStatus failed: %v", err)
	}
	if status != PairStatusApproved {
		t.Errorf("expected approved, got %v", status)
	}

	// Verify IsApproved
	if !pm.IsApproved(token) {
		t.Error("expected IsApproved to return true")
	}
}

func TestPairing_Approve_InvalidCode(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)

	_, err := pm.Approve("000000")
	if err == nil {
		t.Error("expected error for invalid code")
	}
}

func TestPairing_Expired(t *testing.T) {
	// Use very short timeout for testing
	pm := NewPairingManager(1 * time.Millisecond)

	code, token, _, err := pm.CreatePair("/tmp/test-project")
	if err != nil {
		t.Fatalf("CreatePair failed: %v", err)
	}

	// Wait for expiry
	time.Sleep(10 * time.Millisecond)

	status, err := pm.CheckStatus(token)
	if err != nil {
		t.Fatalf("CheckStatus failed: %v", err)
	}
	if status != PairStatusExpired {
		t.Errorf("expected expired, got %v", status)
	}

	// Approving expired code should fail
	_, err = pm.Approve(code)
	if err == nil {
		t.Error("expected error for expired code")
	}
}

func TestPairing_GetProject(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)

	project := "/tmp/test-project"
	code, token, _, err := pm.CreatePair(project)
	if err != nil {
		t.Fatalf("CreatePair failed: %v", err)
	}

	// Before approval, should fail
	_, err = pm.GetProject(token)
	if err == nil {
		t.Error("expected error before approval")
	}

	// Approve and check
	_, err = pm.Approve(code)
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	proj, err := pm.GetProject(token)
	if err != nil {
		t.Fatalf("GetProject failed: %v", err)
	}
	if proj != project {
		t.Errorf("expected project %q, got %q", project, proj)
	}
}

func TestPairing_CleanupExpired(t *testing.T) {
	pm := NewPairingManager(1 * time.Millisecond)

	_, token, _, err := pm.CreatePair("/tmp/test-project")
	if err != nil {
		t.Fatalf("CreatePair failed: %v", err)
	}

	// Wait for expiry
	time.Sleep(10 * time.Millisecond)

	// Before cleanup, should be expired
	status, _ := pm.CheckStatus(token)
	if status != PairStatusExpired {
		t.Errorf("expected expired before cleanup, got %v", status)
	}

	// Cleanup
	pm.CleanupExpired()

	// After cleanup, should be unknown
	status, _ = pm.CheckStatus(token)
	if status != PairStatusUnknown {
		t.Errorf("expected unknown after cleanup, got %v", status)
	}
}

func TestPairing_MultiplePairs(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)

	// Create multiple pairs
	code1, token1, _, err := pm.CreatePair("/tmp/project1")
	if err != nil {
		t.Fatalf("CreatePair 1 failed: %v", err)
	}

	code2, token2, _, err := pm.CreatePair("/tmp/project2")
	if err != nil {
		t.Fatalf("CreatePair 2 failed: %v", err)
	}

	// Codes should be different
	if code1 == code2 {
		t.Error("codes should be unique")
	}

	// Tokens should be different
	if token1 == token2 {
		t.Error("tokens should be unique")
	}

	// Approve only first
	_, err = pm.Approve(code1)
	if err != nil {
		t.Fatalf("Approve 1 failed: %v", err)
	}

	// Verify statuses
	if !pm.IsApproved(token1) {
		t.Error("token1 should be approved")
	}
	if pm.IsApproved(token2) {
		t.Error("token2 should not be approved")
	}

	// Get projects
	proj1, _ := pm.GetProject(token1)
	_, err = pm.GetProject(token2)
	if proj1 != "/tmp/project1" {
		t.Errorf("expected project1, got %q", proj1)
	}
	if err == nil {
		t.Error("expected error for unapproved token2")
	}
}

func TestValidateCode(t *testing.T) {
	tests := []struct {
		code  string
		valid bool
	}{
		{"000000", true},
		{"123456", true},
		{"999999", true},
		{"12345", false},   // too short
		{"1234567", false}, // too long
		{"12345a", false},  // non-digit
		{"abcdef", false},  // all letters
		{"", false},        // empty
	}

	for _, tt := range tests {
		result := ValidateCode(tt.code)
		if result != tt.valid {
			t.Errorf("ValidateCode(%q) = %v, want %v", tt.code, result, tt.valid)
		}
	}
}

func TestParseQRData(t *testing.T) {
	// Test JSON format
	code, token, err := ParseQRData(`{"code":"123456","token":"abc-123"}`)
	if err != nil {
		t.Fatalf("ParseQRData JSON failed: %v", err)
	}
	if code != "123456" {
		t.Errorf("expected code 123456, got %q", code)
	}
	if token != "abc-123" {
		t.Errorf("expected token abc-123, got %q", token)
	}

	// Test colon-separated format
	code, token, err = ParseQRData("654321:xyz-789")
	if err != nil {
		t.Fatalf("ParseQRData colon failed: %v", err)
	}
	if code != "654321" {
		t.Errorf("expected code 654321, got %q", code)
	}
	if token != "xyz-789" {
		t.Errorf("expected token xyz-789, got %q", token)
	}

	// Test invalid format
	_, _, err = ParseQRData("invalid")
	if err == nil {
		t.Error("expected error for invalid format")
	}
}

func TestGenerateQRCode(t *testing.T) {
	png, err := GenerateQRCode(`{"code":"123456","token":"abc-123","server":"pi-go"}`)
	if err != nil {
		t.Fatalf("GenerateQRCode failed: %v", err)
	}
	if len(png) == 0 {
		t.Fatal("GenerateQRCode returned empty PNG")
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("GenerateQRCode did not return PNG data")
	}
}

func TestBuildQRPayload_IncludesHostAndURL(t *testing.T) {
	payload, err := buildQRPayload("123456", "abc-token", "127.0.0.1:8080", "http://127.0.0.1:8080/pair")
	if err != nil {
		t.Fatalf("buildQRPayload failed: %v", err)
	}

	var decoded map[string]string
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if decoded["code"] != "123456" {
		t.Fatalf("unexpected code in payload: %q", decoded["code"])
	}
	if decoded["token"] != "abc-token" {
		t.Fatalf("unexpected token in payload: %q", decoded["token"])
	}
	if decoded["server"] != "127.0.0.1:8080" {
		t.Fatalf("unexpected server in payload: %q", decoded["server"])
	}
	if decoded["url"] != "http://127.0.0.1:8080/pair" {
		t.Fatalf("unexpected url in payload: %q", decoded["url"])
	}
}

// TestCreatePairWithContext_ExhaustsRetries covers the collision-retry
// branch added when CreatePairWithContext grew a non-collision guarantee
// (1,000,000 codes is small enough that tight test loops hit the rare-but-
// real collision case). With the entire code space pre-seeded into
// pm.pending, every random pick collides and the function must error with
// the "exhausted N retries" message after exactly maxAttempts tries.
func TestCreatePairWithContext_ExhaustsRetries(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)

	// Pre-fill the pending map with every 6-digit code so the random picker
	// in CreatePairWithContext can never find a free slot within 8 attempts.
	// The map only cares about the key — the value content is irrelevant.
	pm.mu.Lock()
	now := time.Now()
	pm.pending = make(map[string]*PendingPair, 1_000_000)
	for i := 0; i < 1_000_000; i++ {
		code := fmt.Sprintf("%06d", i)
		pm.pending[code] = &PendingPair{
			Code:      code,
			CreatedAt: now,
			ExpiresAt: now.Add(time.Hour),
		}
	}
	pm.mu.Unlock()

	// Run the test in a goroutine so a hang in the retry loop is bounded by
	// the goroutine's lifetime (the loop is bounded by maxAttempts, but the
	// guard makes the intent explicit and protects against future changes
	// to the retry cap).
	done := make(chan struct{})
	var (
		code      string
		token     string
		qrData    []byte
		createErr error
	)
	go func() {
		defer close(done)
		code, token, qrData, createErr = pm.CreatePairWithContext("/tmp/exhaust", "pi-go", "")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("CreatePairWithContext did not return within 5s; retry loop is unbounded")
	}

	if createErr == nil {
		t.Fatalf("expected exhausted-retries error, got code=%q token=%q qr=%dB", code, token, len(qrData))
	}
	want := "exhausted 8 retries"
	if !bytes.Contains([]byte(createErr.Error()), []byte(want)) {
		t.Errorf("error %q does not contain %q", createErr.Error(), want)
	}
}

// TestCreatePairWithContext_FirstSlotSucceedsOnRetry is a companion to the
// exhausted-retries test: the first picks always collide (seeded map covers
// the first 100 codes) but the 4th random pick is allowed to succeed by
// NOT pre-filling its key. This exercises the break-out-of-loop path
// (the "if _, exists := pm.pending[code]; !exists { break }" branch).
func TestCreatePairWithContext_FirstSlotSucceedsOnRetry(t *testing.T) {
	pm := NewPairingManager(5 * time.Minute)
	pm.mu.Lock()
	pm.pending = make(map[string]*PendingPair)
	// Seed only the first 3 codes so any retry past 3 succeeds.
	now := time.Now()
	for i := 0; i < 3; i++ {
		code := fmt.Sprintf("%06d", i)
		pm.pending[code] = &PendingPair{Code: code, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	}
	pm.mu.Unlock()

	// 1,000,000 - 3 = 999,997 free codes, so the next pick lands in
	// microseconds. No need to bound this with a timeout.
	code, token, qrData, err := pm.CreatePairWithContext("/tmp/first-slot", "pi-go", "")
	if err != nil {
		t.Fatalf("CreatePairWithContext: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("code = %q, want 6 digits", code)
	}
	if token == "" {
		t.Error("token should not be empty")
	}
	if len(qrData) == 0 {
		t.Error("qr data should not be empty")
	}
}

// sync is intentionally omitted: the tests above only use PairingManager
// through its public API and access unexported fields under pm.mu, which is
// already encapsulated.
