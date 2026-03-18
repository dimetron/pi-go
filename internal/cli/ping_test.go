package cli

import (
	"crypto/tls"
	"testing"
)

func TestDefaultAPIBaseURL(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "https://api.anthropic.com"},
		{"openai", "https://api.openai.com"},
		{"gemini", "https://generativelanguage.googleapis.com"},
		{"ollama", ""},
		{"", ""},
		{"unknown", ""},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := defaultAPIBaseURL(tt.provider)
			if got != tt.want {
				t.Errorf("defaultAPIBaseURL(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestPingEndpoint(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "/v1/messages"},
		{"openai", "/v1/models"},
		{"gemini", "/v1beta/models"},
		{"ollama", "/"},
		{"", "/"},
		{"unknown", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := pingEndpoint(tt.provider)
			if got != tt.want {
				t.Errorf("pingEndpoint(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"short string", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"over limit", "hello world", 5, "hello..."},
		{"empty string", "", 5, ""},
		{"zero limit", "hello", 0, "..."},
		{"single char limit", "hello", 1, "h..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncate(tt.s, tt.n)
			if got != tt.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}
}

func TestTLSVersionString(t *testing.T) {
	tests := []struct {
		version uint16
		want    string
	}{
		{tls.VersionTLS10, "1.0"},
		{tls.VersionTLS11, "1.1"},
		{tls.VersionTLS12, "1.2"},
		{tls.VersionTLS13, "1.3"},
		{0x0000, "0x0000"},
		{0xFFFF, "0xffff"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tlsVersionString(tt.version)
			if got != tt.want {
				t.Errorf("tlsVersionString(0x%04x) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}

func TestNewPingCmd(t *testing.T) {
	cmd := newPingCmd()
	if cmd.Use != "ping [prompt...]" {
		t.Errorf("unexpected Use: %s", cmd.Use)
	}
	// Verify flags exist.
	flags := []string{"model", "url", "smol", "slow", "plan"}
	for _, name := range flags {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("missing flag: %s", name)
		}
	}
}
