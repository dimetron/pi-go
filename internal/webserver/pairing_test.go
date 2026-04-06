package webserver

import (
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
