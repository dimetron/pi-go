package cli

import (
	"context"
	"fmt"
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

func TestFetchLatestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got == "" {
			t.Error("expected User-Agent header")
		}
		fmt.Fprint(w, `{"tag_name":"v1.2.3"}`)
	}))
	defer srv.Close()

	version, err := fetchLatestVersion(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("fetchLatestVersion: %v", err)
	}
	if version != "v1.2.3" {
		t.Fatalf("version = %q, want v1.2.3", version)
	}
}

func TestFetchLatestVersionHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	if _, err := fetchLatestVersion(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("expected HTTP status error")
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    bool
	}{
		{"1.2.3", "v1.2.4", true},
		{"1.2.3+abc123", "v1.3.0", true},
		{"1.2.3", "v1.2.3", false},
		{"1.2.3", "v1.2.2", false},
		{"dev", "v1.2.3", false},
		{"1.10.0", "v1.9.9", false},
	}
	for _, tt := range tests {
		t.Run(tt.current+"/"+tt.latest, func(t *testing.T) {
			if got := isNewerVersion(tt.current, tt.latest); got != tt.want {
				t.Fatalf("isNewerVersion(%q, %q) = %v, want %v", tt.current, tt.latest, got, tt.want)
			}
		})
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
