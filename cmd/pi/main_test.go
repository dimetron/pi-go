package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestIsOTELPortAvailableWhenClosed(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:1")
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	if isOTELPortAvailable() {
		t.Fatal("expected closed port to be unavailable")
	}
	if got := os.Getenv("OTEL_TRACES_EXPORTER"); got != "none" {
		t.Fatalf("OTEL_TRACES_EXPORTER = %q, want none", got)
	}
}

func TestIsOTELPortAvailableWhenListening(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
		}
	}()

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:"+strconv.Itoa(port))
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	if !isOTELPortAvailable() {
		t.Fatal("expected listening port to be available")
	}
	if got := os.Getenv("OTEL_TRACES_EXPORTER"); got != "otlp" {
		t.Fatalf("OTEL_TRACES_EXPORTER = %q, want otlp", got)
	}
}

// run is main's body. A CLI error must be reported on stderr and produce a
// non-zero exit code; a clean run must exit 0 after flushing traces.
func TestRun_ExitCodes(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var stderr bytes.Buffer
		code := run(&stderr, func() error { return nil })
		if code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty on success", stderr.String())
		}
	})

	t.Run("cli error", func(t *testing.T) {
		var stderr bytes.Buffer
		code := run(&stderr, func() error { return errors.New("boom") })
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "boom") {
			t.Errorf("stderr = %q, want it to carry the error", stderr.String())
		}
	})
}
