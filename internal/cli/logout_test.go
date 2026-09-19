package cli

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/testenv"
)

// xaiCLIClientID is the public Grok-CLI client id the auth package registers.
// Duplicated here rather than exported from auth, because exporting a constant
// only a test needs would widen the package's surface for no production gain.
const xaiCLIClientID = "b1a00492-073a-47ea-816f-4c329264a828"

// makeXAIToken builds a JWT-shaped token with the given claims. The auth
// package's equivalent lives in a _test.go file and so is not importable from
// here, and the shape matters because both the token store and the grok-CLI
// importer gate on it.
func makeXAIToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		body, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(body)
	}
	return enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." +
		enc(claims) + ".signature-not-verified"
}

// writeGrokAuth writes a ~/.grok/auth.json carrying the given entry, so the
// adoption path in runLogin has something to import.
func writeGrokAuth(t *testing.T, home string, entry map[string]any) {
	t.Helper()
	dir := filepath.Join(home, ".grok")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.Marshal(map[string]any{
		"https://auth.x.ai::" + xaiCLIClientID: entry,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), raw, 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestNewLogoutCmd_Structure(t *testing.T) {
	cmd := newLogoutCmd()
	if cmd.Use != "logout [provider]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "logout [provider]")
	}
	if !strings.Contains(cmd.Long, "xai") {
		t.Error("Long help should name the supported provider")
	}
}

func TestRunLogout_RequiresAProvider(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	err := newLogoutCmd().RunE(nil, nil)
	if err == nil {
		t.Fatal("expected an error when no provider is given")
	}
	if !strings.Contains(err.Error(), "a provider is required") {
		t.Errorf("err = %v, want it to ask for a provider", err)
	}
}

func TestRunLogout_UnknownProvider(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	err := newLogoutCmd().RunE(nil, []string{"not-a-real-provider"})
	if err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("err = %v, want 'unknown provider'", err)
	}
}

// TestRunLogout_RemovesTheTokenStore covers the supported path end to end: a
// stored subscription credential is removed, and the message says so. This is
// the whole point of logout — a user who cannot revoke the stored refresh
// token has no way to stop a machine from refreshing it.
func TestRunLogout_RemovesTheTokenStore(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	if err := auth.SaveXAITokens(&auth.XAITokenSet{
		AccessToken:  "access",
		RefreshToken: "refresh",
	}); err != nil {
		t.Fatalf("SaveXAITokens: %v", err)
	}

	out := captureStdout(t, func() {
		if err := newLogoutCmd().RunE(nil, []string{"xai"}); err != nil {
			t.Fatalf("runLogout: %v", err)
		}
	})

	if !strings.Contains(out, "Removed the stored xAI subscription token.") {
		t.Errorf("output = %q, want the removal confirmation", out)
	}
	// The developer key is deliberately left behind, and the user is told so.
	if !strings.Contains(out, "XAI_API_KEY") {
		t.Errorf("output = %q, want it to mention the untouched developer key", out)
	}

	stored, err := auth.LoadXAITokens()
	if err != nil {
		t.Fatalf("LoadXAITokens: %v", err)
	}
	if stored != nil {
		t.Errorf("token store still holds %+v after logout", stored)
	}
}

// TestRunLogout_IsIdempotent: logging out twice is not an error. A user who
// runs logout on a machine that was never logged in should get a clean result,
// not a failure they have to interpret.
func TestRunLogout_IsIdempotent(t *testing.T) {
	testenv.SetHome(t, t.TempDir())
	for i := range 2 {
		out := captureStdout(t, func() {
			if err := newLogoutCmd().RunE(nil, []string{"xai"}); err != nil {
				t.Fatalf("runLogout call %d: %v", i+1, err)
			}
		})
		if !strings.Contains(out, "Removed") {
			t.Errorf("call %d output = %q, want the removal confirmation", i+1, out)
		}
	}
}

// TestRunLogout_UnresolvableHome: when the home directory cannot be resolved
// there is nothing to remove, and reporting success would tell the user a
// credential was revoked when it was not.
func TestRunLogout_UnresolvableHome(t *testing.T) {
	testenv.UnsetHome(t)
	err := newLogoutCmd().RunE(nil, []string{"xai"})
	if err == nil {
		t.Fatal("expected an error when home cannot be resolved")
	}
}

// TestSaveAdoptedXAI_ReportsWriteFailures: both writes in the adoption path can
// fail, and each must be reported rather than swallowed. A half-saved
// credential — store written, env var not — leaves the next run unable to find
// the token it was just told about.
func TestSaveAdoptedXAI_ReportsWriteFailures(t *testing.T) {
	t.Run("token store cannot be written", func(t *testing.T) {
		home := t.TempDir()
		testenv.SetHome(t, home)
		// A regular file where the config directory belongs: MkdirAll fails.
		if err := os.WriteFile(filepath.Join(home, ".pi-go"), nil, 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
		err := saveAdoptedXAI(&auth.XAITokenSet{
			AccessToken: "access",
			ExpiresAt:   time.Now().Add(time.Hour),
		})
		if err == nil {
			t.Fatal("expected an error when the token store cannot be written")
		}
		if !strings.Contains(err.Error(), "saving xAI credentials") {
			t.Errorf("err = %v, want it to name the credential save", err)
		}
	})

	t.Run("env file cannot be written", func(t *testing.T) {
		home := t.TempDir()
		testenv.SetHome(t, home)
		// The token store succeeds, but ~/.pi-go/.env cannot be created.
		if err := os.MkdirAll(filepath.Join(home, ".pi-go"), 0700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(home, ".pi-go", ".env"), []byte("x"), 0400); err != nil {
			t.Fatalf("write .env: %v", err)
		}
		// Make the .env read-only so updateEnvVar cannot rewrite it.
		requirePOSIXPerms(t)

		err := saveAdoptedXAI(&auth.XAITokenSet{
			AccessToken: "access",
			ExpiresAt:   time.Now().Add(time.Hour),
		})
		if err == nil {
			t.Fatal("expected an error when ~/.pi-go/.env cannot be rewritten")
		}
		if !strings.Contains(err.Error(), "saving xAI key") {
			t.Errorf("err = %v, want it to name the key save", err)
		}
	})
}

// requirePOSIXPerms skips tests that rely on permission bits being enforced:
// Windows ignores them and root bypasses them.
func requirePOSIXPerms(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
}

// TestRunLogin_AdoptsGrokCLISession covers the adoption path: a user who
// already authenticated with the official `grok` CLI is not asked to log in
// again. The credential must be both stored (refreshable) and exported as the
// env var the provider reads.
func TestRunLogin_AdoptsGrokCLISession(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	t.Setenv("XAI_API_KEY", "")

	exp := time.Now().Add(3 * time.Hour).UTC().Truncate(time.Second)
	writeGrokAuth(t, home, map[string]any{
		"key": makeXAIToken(t, map[string]any{
			"iss":          "https://auth.x.ai",
			"principal_id": "acct-cli-adopt",
			"exp":          exp.Unix(),
		}),
		"refresh_token": "cli-refresh",
		"expires_at":    exp.Format(time.RFC3339),
	})

	out := captureStdout(t, func() {
		if err := newLoginCmd().RunE(nil, []string{"xai"}); err != nil {
			t.Fatalf("runLogin: %v", err)
		}
	})
	if !strings.Contains(out, "Imported your existing grok CLI session.") {
		t.Errorf("output = %q, want the import confirmation", out)
	}
	// The expiry is reported so the user can see the credential is live and
	// being refreshed rather than a static value they must babysit.
	if !strings.Contains(out, "valid until") {
		t.Errorf("output = %q, want the expiry line", out)
	}

	stored, err := auth.LoadXAITokens()
	if err != nil {
		t.Fatalf("LoadXAITokens: %v", err)
	}
	if stored == nil {
		t.Fatal("adoption did not persist the token set")
	}
	if stored.RefreshToken != "cli-refresh" {
		t.Errorf("RefreshToken = %q, want cli-refresh", stored.RefreshToken)
	}
	if got := os.Getenv("XAI_API_KEY"); got == "" {
		t.Error("XAI_API_KEY was not exported for this process")
	}
}

// TestRunLogin_AdoptedTokenSurvivesAnEmptyExpiry: an adopted token with no
// expires_at is still saved. Its zero expiry is reported as nothing rather
// than as a bogus 1970 date, and the store treats it as stale so the refresh
// grant renews it on first use.
func TestRunLogin_AdoptedTokenSurvivesAnEmptyExpiry(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	t.Setenv("XAI_API_KEY", "")

	writeGrokAuth(t, home, map[string]any{
		"key": makeXAIToken(t, map[string]any{"iss": "https://auth.x.ai"}),
	})

	out := captureStdout(t, func() {
		if err := newLoginCmd().RunE(nil, []string{"xai"}); err != nil {
			t.Fatalf("runLogin: %v", err)
		}
	})
	if !strings.Contains(out, "Imported your existing grok CLI session.") {
		t.Errorf("output = %q, want the import confirmation", out)
	}
	// No expiry means no expiry line: printing "valid until 0001-01-01" would
	// read as an expired credential.
	if strings.Contains(out, "valid until") {
		t.Errorf("output = %q, want no expiry line for a token with no exp claim", out)
	}

	if stored, err := auth.LoadXAITokens(); err != nil {
		t.Fatalf("LoadXAITokens: %v", err)
	} else if stored == nil {
		t.Fatal("adoption did not persist the token set")
	}
}
