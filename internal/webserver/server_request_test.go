package webserver

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestOriginAndHost_ForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/pair", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "example.com")

	origin, host := requestOriginAndHost(req)
	if origin != "https://example.com" {
		t.Fatalf("unexpected origin: %q", origin)
	}
	if host != "example.com" {
		t.Fatalf("unexpected host: %q", host)
	}
}

func TestRequestOriginAndHost_DefaultsToRequestHost(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:8080/api/pair", nil)

	origin, host := requestOriginAndHost(req)
	if origin != "http://127.0.0.1:8080" {
		t.Fatalf("unexpected origin: %q", origin)
	}
	if host != "127.0.0.1:8080" {
		t.Fatalf("unexpected host: %q", host)
	}
}

func TestApproveActivePairCode_MatchingCodeApproves(t *testing.T) {
	s := NewServerV2(Config{
		Addr:           "127.0.0.1:0",
		PairingTimeout: 5 * time.Minute,
		Project:        ".",
	})

	code, token, err := s.BootstrapPair(".")
	if err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}

	gotToken, err := s.approveActivePairCode(code)
	if err != nil {
		t.Fatalf("approveActivePairCode: %v", err)
	}
	if gotToken != token {
		t.Fatalf("unexpected token: got %q want %q", gotToken, token)
	}

	status, err := s.PairingManager().CheckStatus(token)
	if err != nil {
		t.Fatalf("CheckStatus: %v", err)
	}
	if status != PairStatusApproved {
		t.Fatalf("expected approved, got %q", status)
	}
}

func TestApproveActivePairCode_MismatchFails(t *testing.T) {
	s := NewServerV2(Config{
		Addr:           "127.0.0.1:0",
		PairingTimeout: 5 * time.Minute,
		Project:        ".",
	})

	if _, _, err := s.BootstrapPair("."); err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}

	if _, err := s.approveActivePairCode("000000"); err == nil {
		t.Fatal("expected invalid code error")
	}
}

func TestApproveActivePairCode_AllowsAnyPendingCode(t *testing.T) {
	s := NewServerV2(Config{
		Addr:           "127.0.0.1:0",
		PairingTimeout: 5 * time.Minute,
		Project:        ".",
	})

	if _, _, err := s.BootstrapPair("."); err != nil {
		t.Fatalf("BootstrapPair: %v", err)
	}

	otherCode, otherToken, err := s.PairingManager().CreatePair("/tmp/other-project")
	if err != nil {
		t.Fatalf("CreatePair: %v", err)
	}

	gotToken, err := s.approveActivePairCode(otherCode)
	if err != nil {
		t.Fatalf("approveActivePairCode: %v", err)
	}
	if gotToken != otherToken {
		t.Fatalf("unexpected token: got %q want %q", gotToken, otherToken)
	}
}
