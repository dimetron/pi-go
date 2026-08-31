package server

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2aclient "github.com/a2aproject/a2a-go/v2/a2aclient"

	acpserver "github.com/dimetron/pi-go/internal/acp/server"
)

// startTestServer runs Serve on an ephemeral port with the echo handler and
// returns the base URL and a stop function.
func startTestServer(t *testing.T) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // let Serve rebind the same address

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, ServeConfig{
			Addr:    addr,
			Handler: acpserver.EchoPromptHandler,
		})
	}()

	// Wait for the server to accept connections.
	base := "http://" + addr
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base, func() {
					cancel()
					select {
					case <-done:
					case <-time.After(3 * time.Second):
					}
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	t.Fatalf("server did not become ready")
	return "", nil
}

func TestServeAgentCard(t *testing.T) {
	base, stop := startTestServer(t)
	defer stop()

	resp, err := http.Get(base + "/.well-known/agent-card.json")
	if err != nil {
		t.Fatalf("GET card: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("card status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "json") {
		t.Fatalf("card content-type = %q, want json", ct)
	}
}

func TestServeSendMessage(t *testing.T) {
	base, stop := startTestServer(t)
	defer stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cli, err := a2aclient.NewFromEndpoints(ctx, []*a2a.AgentInterface{
		a2a.NewAgentInterface(base+"/", a2a.TransportProtocolJSONRPC),
	})
	if err != nil {
		t.Fatalf("NewFromEndpoints: %v", err)
	}
	defer cli.Destroy()

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello a2a"))
	res, err := cli.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	got := ""
	switch r := res.(type) {
	case *a2a.Message:
		for _, part := range r.Parts {
			if s := part.Text(); s != "" {
				got += s
			}
		}
	case *a2a.Task:
		if r.Status.State != a2a.TaskStateCompleted {
			t.Fatalf("task state = %s, want %s", r.Status.State, a2a.TaskStateCompleted)
		}
		for _, art := range r.Artifacts {
			for _, part := range art.Parts {
				if s := part.Text(); s != "" {
					got += s
				}
			}
		}
	default:
		t.Fatalf("result type = %T, want *a2a.Message or *a2a.Task", res)
	}
	if !strings.Contains(got, "echo: hello a2a") {
		t.Fatalf("result text = %q, want it to contain %q", got, "echo: hello a2a")
	}
}

func TestServeHealth(t *testing.T) {
	base, stop := startTestServer(t)
	defer stop()

	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
}
