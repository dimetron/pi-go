package main

import (
	"net"
	"os"
	"strconv"
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
