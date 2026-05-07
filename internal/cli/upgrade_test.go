package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewUpgradeCmd(t *testing.T) {
	cmd := newUpgradeCmd()
	if cmd.Use != "upgrade" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	if cmd.Short == "" || cmd.Long == "" {
		t.Fatal("expected help text")
	}
	if cmd.RunE == nil {
		t.Fatal("expected RunE")
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Fatal("expected NoArgs error")
	}
}

func TestRunUpgradePowerShellFetchError(t *testing.T) {
	if err := runUpgradePowerShell("http://127.0.0.1:1/nope.ps1"); err == nil {
		t.Fatal("expected fetch error")
	}
}

func TestRunUpgradePowerShellHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if err := runUpgradePowerShell(srv.URL); err == nil {
		t.Fatal("expected HTTP status error")
	}
}
