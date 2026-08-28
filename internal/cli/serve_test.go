package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/webserver"
)

// -----------------------------------------------------------------------
// ParsePairingCode — edge cases and extended coverage
// -----------------------------------------------------------------------

func TestParsePairingCode_EmptyString(t *testing.T) {
	code, token, err := ParsePairingCode("")
	if err != nil {
		t.Fatalf("ParsePairingCode('') unexpected error: %v", err)
	}
	if code != "" {
		t.Errorf("code = %q, want empty string", code)
	}
	if token != "" {
		t.Errorf("token = %q, want empty string", token)
	}
}

func TestParsePairingCode_MultipleColons(t *testing.T) {
	// "a:b:c" splits into ["a","b","c"], len != 2 so treated as plain code
	code, token, err := ParsePairingCode("a:b:c")
	if err != nil {
		t.Fatalf("ParsePairingCode unexpected error: %v", err)
	}
	if code != "a:b:c" {
		t.Errorf("code = %q, want 'a:b:c'", code)
	}
	if token != "" {
		t.Errorf("token = %q, want empty", token)
	}
}

func TestParsePairingCode_ColonAtStart(t *testing.T) {
	// ":token" should return "", "token"
	code, token, err := ParsePairingCode(":token")
	if err != nil {
		t.Fatalf("ParsePairingCode unexpected error: %v", err)
	}
	if code != "" {
		t.Errorf("code = %q, want empty", code)
	}
	if token != "token" {
		t.Errorf("token = %q, want 'token'", token)
	}
}

func TestParsePairingCode_ColonAtEnd(t *testing.T) {
	// "code:" should return "code", ""
	code, token, err := ParsePairingCode("code:")
	if err != nil {
		t.Fatalf("ParsePairingCode unexpected error: %v", err)
	}
	if code != "code" {
		t.Errorf("code = %q, want 'code'", code)
	}
	if token != "" {
		t.Errorf("token = %q, want empty", token)
	}
}

func TestParsePairingCode_TokenWithColons(t *testing.T) {
	// "code:token:with:colons" splits on all ':' so we get more than 2 parts
	// len > 2, so treated as plain code (current implementation)
	code, _, err := ParsePairingCode("code:token:with:colons")
	if err != nil {
		t.Fatalf("ParsePairingCode unexpected error: %v", err)
	}
	if code != "code:token:with:colons" {
		t.Errorf("code = %q, want 'code:token:with:colons'", code)
	}
}

func TestParsePairingCode_WhitespaceOnly(t *testing.T) {
	code, _, err := ParsePairingCode("   ")
	if err != nil {
		t.Fatalf("ParsePairingCode unexpected error: %v", err)
	}
	if code != "" {
		t.Errorf("code = %q, want empty", code)
	}
}

func TestParsePairingCode_TabsAndSpaces(t *testing.T) {
	// TrimSpace only trims leading/trailing of whole input, not around ':'
	code, token, err := ParsePairingCode("\t code : token \t")
	if err != nil {
		t.Fatalf("ParsePairingCode unexpected error: %v", err)
	}
	if code != "code " {
		t.Errorf("code = %q, want 'code '", code)
	}
	if token != " token" {
		t.Errorf("token = %q, want ' token'", token)
	}
}

// -----------------------------------------------------------------------
// GetServePairingManager — additional coverage
// -----------------------------------------------------------------------

func TestGetServePairingManager_MultipleServers(t *testing.T) {
	// Create two servers and verify each returns a distinct (or same) manager
	// but both are non-nil
	server1 := webserver.NewServerV2(webserver.Config{
		PairingTimeout: 5 * time.Minute,
	})
	server2 := webserver.NewServerV2(webserver.Config{
		PairingTimeout: 10 * time.Minute,
	})

	pm1 := GetServePairingManager(server1)
	pm2 := GetServePairingManager(server2)

	if pm1 == nil {
		t.Error("server1 PairingManager is nil")
	}
	if pm2 == nil {
		t.Error("server2 PairingManager is nil")
	}
}

// -----------------------------------------------------------------------
// newServeCmd — extended flag and structure tests
// -----------------------------------------------------------------------

func TestNewServeCmd_FlagDefaults(t *testing.T) {
	cmd := newServeCmd()

	// Check addr default
	addrVal, err := cmd.Flags().GetString("addr")
	if err != nil {
		t.Fatalf("getting addr flag: %v", err)
	}
	if addrVal != webserver.DefaultAddr {
		t.Errorf("addr default = %q, want %q", addrVal, webserver.DefaultAddr)
	}

	// Check pairing-timeout default
	timeoutVal, err := cmd.Flags().GetDuration("pairing-timeout")
	if err != nil {
		t.Fatalf("getting pairing-timeout flag: %v", err)
	}
	if timeoutVal != 5*time.Minute {
		t.Errorf("pairing-timeout default = %v, want 5m", timeoutVal)
	}

	// Check model default is empty
	modelVal, err := cmd.Flags().GetString("model")
	if err != nil {
		t.Fatalf("getting model flag: %v", err)
	}
	if modelVal != "" {
		t.Errorf("model default = %q, want empty", modelVal)
	}

	// Check url default is empty
	urlVal, err := cmd.Flags().GetString("url")
	if err != nil {
		t.Fatalf("getting url flag: %v", err)
	}
	if urlVal != "" {
		t.Errorf("url default = %q, want empty", urlVal)
	}

	// Check project default is empty (uses cwd)
	projectVal, err := cmd.Flags().GetString("project")
	if err != nil {
		t.Fatalf("getting project flag: %v", err)
	}
	if projectVal != "" {
		t.Errorf("project default = %q, want empty", projectVal)
	}
}

func TestNewServeCmd_FlagParsing(t *testing.T) {
	cmd := newServeCmd()
	args := []string{
		"--addr", "localhost:9999",
		"--project", "/custom/path",
		"--pairing-timeout", "10m",
		"--model", "gpt-5",
	}
	cmd.SetArgs(args)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags error: %v", err)
	}

	addrVal, _ := cmd.Flags().GetString("addr")
	if addrVal != "localhost:9999" {
		t.Errorf("addr = %q, want 'localhost:9999'", addrVal)
	}

	projectVal, _ := cmd.Flags().GetString("project")
	if projectVal != "/custom/path" {
		t.Errorf("project = %q, want '/custom/path'", projectVal)
	}

	timeoutVal, _ := cmd.Flags().GetDuration("pairing-timeout")
	if timeoutVal != 10*time.Minute {
		t.Errorf("pairing-timeout = %v, want 10m", timeoutVal)
	}

	modelVal, _ := cmd.Flags().GetString("model")
	if modelVal != "gpt-5" {
		t.Errorf("model = %q, want 'gpt-5'", modelVal)
	}
}

func TestNewServeCmd_HeaderInsecureFlags(t *testing.T) {
	cmd := newServeCmd()
	args := []string{
		"--header", "Authorization=Bearer x",
		"--header", "X-Trace=abc",
		"--insecure",
		"--url", "https://example.test",
	}
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags error: %v", err)
	}

	headers, _ := cmd.Flags().GetStringArray("header")
	if len(headers) != 2 || headers[0] != "Authorization=Bearer x" || headers[1] != "X-Trace=abc" {
		t.Errorf("headers = %v, want [Authorization=Bearer x X-Trace=abc]", headers)
	}

	insecure, _ := cmd.Flags().GetBool("insecure")
	if !insecure {
		t.Error("insecure should be true")
	}

	urlVal, _ := cmd.Flags().GetString("url")
	if urlVal != "https://example.test" {
		t.Errorf("url = %q, want 'https://example.test'", urlVal)
	}
}

func TestRunServe_InvalidHeaderReturnsError(t *testing.T) {
	oldHeaders := flagServeHeaders
	t.Cleanup(func() { flagServeHeaders = oldHeaders })
	flagServeHeaders = []string{"no-equals-sign"}

	err := runServe(nil, nil)
	if err == nil {
		t.Fatal("expected error for header without '='")
	}
	if !strings.Contains(err.Error(), "key=value") {
		t.Errorf("expected key=value hint, got %v", err)
	}
}

func TestNewServeCmd_ShortAndLong(t *testing.T) {
	cmd := newServeCmd()
	if cmd.Short == "" {
		t.Error("Short description is empty")
	}
	if cmd.Long == "" {
		t.Error("Long description is empty")
	}
	if cmd.Use != "serve" {
		t.Errorf("Use = %q, want 'serve'", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil")
	}
}

// -----------------------------------------------------------------------
// Listen address: loopback by default, network exposure opt-in
// -----------------------------------------------------------------------

// `pi serve` hands whoever pairs with it a terminal running the agent, so the
// default --addr must not answer on every interface.
func TestServeAddrDefaultIsLoopback(t *testing.T) {
	if !isLoopbackAddr(webserver.DefaultAddr) {
		t.Errorf("default --addr %q is not loopback-only", webserver.DefaultAddr)
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	tests := []struct {
		addr string
		want bool
	}{
		{addr: "127.0.0.1:8765", want: true},
		{addr: "localhost:8765", want: true},
		{addr: "[::1]:8765", want: true},
		{addr: "127.0.0.2:8765", want: true},
		{addr: ":8765", want: false},
		{addr: "0.0.0.0:8765", want: false},
		{addr: "[::]:8765", want: false},
		{addr: "192.168.1.10:8765", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			if got := isLoopbackAddr(tt.addr); got != tt.want {
				t.Errorf("isLoopbackAddr(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

// The banner has to say the server is local-only, or the operator debugs a
// phone that cannot load the pairing page.
func TestPrintServeBanner_LoopbackNotice(t *testing.T) {
	var buf strings.Builder
	printServeBanner(&buf, "127.0.0.1:8765", "/proj", "123456")
	if !strings.Contains(buf.String(), "Reachable from this machine only") {
		t.Errorf("loopback banner missing the local-only notice:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "--addr 0.0.0.0:8765") {
		t.Errorf("loopback banner does not say how to expose the server:\n%s", buf.String())
	}

	buf.Reset()
	printServeBanner(&buf, "0.0.0.0:8765", "/proj", "123456")
	if strings.Contains(buf.String(), "Reachable from this machine only") {
		t.Errorf("wildcard bind should not claim to be local-only:\n%s", buf.String())
	}
}
