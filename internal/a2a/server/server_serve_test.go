package server

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"

	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"

	acpserver "github.com/dimetron/pi-go/internal/acp/server"
)

// freeAddr returns a loopback address that was bound and released, so Serve
// can rebind it without colliding with another test.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return addr
}

// startServer runs Serve on ephemeral ports and returns both base URLs.
func startServer(t *testing.T) (base, readyBase string) {
	t.Helper()
	addr, readyAddr := freeAddr(t), freeAddr(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeConfig{
			Addr:      addr,
			ReadyAddr: readyAddr,
			Handler:   acpserver.EchoPromptHandler,
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("Serve did not return after context cancel")
		}
	})

	base, readyBase = "http://"+addr, "http://"+readyAddr
	waitReady(t, base+"/health")
	return base, readyBase
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not become ready", url)
}

func TestServeRequiresHandler(t *testing.T) {
	err := Serve(context.Background(), ServeConfig{Addr: "127.0.0.1:0"})
	if err == nil || !strings.Contains(err.Error(), "handler is required") {
		t.Fatalf("Serve() error = %v, want a handler-is-required error", err)
	}
}

func TestServeRejectsBadAddr(t *testing.T) {
	err := Serve(context.Background(), ServeConfig{
		Addr:    "127.0.0.1:not-a-port",
		Handler: acpserver.EchoPromptHandler,
	})
	if err == nil || !strings.Contains(err.Error(), "listen") {
		t.Fatalf("Serve() error = %v, want a listen error", err)
	}
}

func TestServeRejectsBadReadyAddr(t *testing.T) {
	err := Serve(context.Background(), ServeConfig{
		Addr:      freeAddr(t),
		ReadyAddr: "127.0.0.1:not-a-port",
		Handler:   acpserver.EchoPromptHandler,
	})
	if err == nil || !strings.Contains(err.Error(), "listen ready") {
		t.Fatalf("Serve() error = %v, want a ready-listener error", err)
	}
}

func TestServeReturnsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeConfig{
			Addr:      freeAddr(t),
			ReadyAddr: freeAddr(t),
			Handler:   acpserver.EchoPromptHandler,
		})
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !strings.Contains(err.Error(), context.Canceled.Error()) {
			t.Fatalf("Serve() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after context cancel")
	}
}

func TestServeHealthEndpoints(t *testing.T) {
	base, _ := startServer(t)

	for _, path := range []string{"/health", "/healthz"} {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(base + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200", resp.StatusCode)
			}
		})
	}
}

func TestServeReadyz(t *testing.T) {
	_, readyBase := startServer(t)

	t.Run("readyz is served", func(t *testing.T) {
		resp, err := http.Get(readyBase + "/readyz")
		if err != nil {
			t.Fatalf("GET /readyz: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("other paths are 404", func(t *testing.T) {
		resp, err := http.Get(readyBase + "/nope")
		if err != nil {
			t.Fatalf("GET /nope: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})
}

func TestServeGRPCHealth(t *testing.T) {
	base, _ := startServer(t)
	target := strings.TrimPrefix(base, "http://")

	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := grpc_health_v1.NewHealthClient(conn).Check(ctx,
		&grpc_health_v1.HealthCheckRequest{Service: a2apb.A2AService_ServiceDesc.ServiceName})
	if err != nil {
		t.Fatalf("grpc health check: %v", err)
	}
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("status = %v, want SERVING", resp.Status)
	}
}
