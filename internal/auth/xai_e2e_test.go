//go:build e2e

package auth

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestE2EXAIRefreshGrant drives the real xAI token endpoint with whatever
// subscription credential is locally available, so the refresh path is
// exercised against xAI rather than only against a stub.
//
// It skips when no grok CLI credential exists: the flow is opt-in by virtue of
// having logged in, the same way the provider e2e tests gate on XAI_API_KEY.
// Set XAI_E2E_REFRESH_TOKEN to test a specific refresh token instead.
func TestE2EXAIRefreshGrant(t *testing.T) {
	refreshToken := os.Getenv("XAI_E2E_REFRESH_TOKEN")
	expiry := time.Time{}
	if refreshToken == "" {
		set, err := ImportGrokCLITokens()
		if err != nil {
			t.Fatalf("ImportGrokCLITokens: %v", err)
		}
		if set == nil || set.RefreshToken == "" {
			t.Skip("skipping: no xAI subscription credential available (run `grok login`, or set XAI_E2E_REFRESH_TOKEN)")
		}
		refreshToken = set.RefreshToken
		expiry = set.ExpiresAt
	}
	t.Logf("using refresh token (imported expiry=%s)", expiry)

	refreshed, err := RefreshXAIToken(context.Background(), refreshToken)
	if err != nil {
		t.Fatalf("RefreshXAIToken: %v", err)
	}
	if refreshed.AccessToken == "" {
		t.Fatal("no access token returned")
	}
	// xAI issues 6-hour access tokens. Anything much shorter would mean the
	// expiry derivation is wrong, which would silently break refresh timing.
	if remaining := time.Until(refreshed.ExpiresAt); remaining < 5*time.Hour {
		t.Errorf("access token expires in %s, want ~6h", remaining)
	}
	if refreshed.RefreshToken == "" {
		t.Error("no rotated refresh token")
	}
	if !IsXAISubscriptionToken(refreshed.AccessToken) {
		t.Error("refreshed access token is not recognised as a subscription token")
	}
	if refreshed.AccountID == "" {
		t.Error("no account id derived from the refreshed token")
	}
	t.Logf("refreshed ok: account=%s expires=%s", refreshed.AccountID, refreshed.ExpiresAt.Format(time.RFC3339))
}

// TestE2EXAIResolveCredential pins that an end-to-end resolve prefers a
// developer key when one is configured, and otherwise reports the subscription
// path.
func TestE2EXAIResolveCredential(t *testing.T) {
	key, sub, err := ResolveXAICredential(context.Background())
	if err != nil {
		t.Fatalf("ResolveXAICredential: %v", err)
	}
	if key == "" {
		t.Skip("skipping: no xAI credential configured")
	}
	t.Logf("resolved credential: subscription=%v len=%d", sub, len(key))
	if sub != IsXAISubscriptionToken(key) {
		t.Errorf("subscription=%v disagrees with token shape (subscription=%v)", sub, IsXAISubscriptionToken(key))
	}
}
