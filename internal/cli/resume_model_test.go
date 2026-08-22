package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/testenv"
)

// writeSessionMeta creates a session directory whose meta.json records model.
func writeSessionMeta(t *testing.T, dir, sessionID, model string) {
	t.Helper()
	sessionDir := filepath.Join(dir, ".pi-go", "sessions", sessionID)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"id":"` + sessionID + `","appName":"pi-go","userID":"local","model":"` + model + `"}`
	if err := os.WriteFile(filepath.Join(sessionDir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
}

// withFlags restores the package-level flag state the CLI resolves through.
func withFlags(t *testing.T, session, model string) {
	t.Helper()
	origSession, origModel := flagSession, flagModel
	t.Cleanup(func() { flagSession, flagModel = origSession, origModel })
	flagSession, flagModel = session, model
}

func TestApplyResumedModel(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	writeSessionMeta(t, home, "sess-gpt", "gpt-5.6-luna")
	writeSessionMeta(t, home, "sess-unknown", "unknown")

	tests := []struct {
		name       string
		session    string
		model      string
		activeRole string
		want       string
	}{
		{"restores the session's model", "sess-gpt", "", "default", "gpt-5.6-luna"},
		{"--model wins", "sess-gpt", "gemini-3.5-pro", "default", "claude-sonnet-4-6"},
		{"role flag wins", "sess-gpt", "", "plan", "claude-sonnet-4-6"},
		{"unknown model is ignored", "sess-unknown", "", "default", "claude-sonnet-4-6"},
		{"missing session is ignored", "sess-absent", "", "default", "claude-sonnet-4-6"},
		{"no session is ignored", "", "", "default", "claude-sonnet-4-6"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withFlags(t, tc.session, tc.model)
			cfg := config.Config{Roles: map[string]config.RoleConfig{
				"default": {Model: "claude-sonnet-4-6", Provider: "anthropic"},
			}}

			applyResumedModel(&cfg, tc.activeRole)

			if got := cfg.Roles["default"].Model; got != tc.want {
				t.Errorf("model = %q, want %q", got, tc.want)
			}
		})
	}
}

// The configured provider belongs to the configured model; carrying it over
// would route the restored model to the wrong API.
func TestApplyResumedModel_ClearsStaleProvider(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	writeSessionMeta(t, home, "sess-gpt", "gpt-5.6-luna")
	withFlags(t, "sess-gpt", "")

	cfg := config.Config{Roles: map[string]config.RoleConfig{
		"default": {Model: "claude-sonnet-4-6", Provider: "anthropic"},
	}}

	applyResumedModel(&cfg, "default")

	if got := cfg.Roles["default"].Provider; got != "" {
		t.Errorf("provider = %q, want it cleared for re-detection", got)
	}
}

// A session already running the configured model must not have its role
// rewritten, or an explicitly configured provider would be dropped for nothing.
func TestApplyResumedModel_KeepsProviderWhenModelMatches(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	writeSessionMeta(t, home, "sess-same", "claude-sonnet-4-6")
	withFlags(t, "sess-same", "")

	cfg := config.Config{Roles: map[string]config.RoleConfig{
		"default": {Model: "claude-sonnet-4-6", Provider: "anthropic"},
	}}

	applyResumedModel(&cfg, "default")

	if got := cfg.Roles["default"].Provider; got != "anthropic" {
		t.Errorf("provider = %q, want it left alone", got)
	}
}
