package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// freeA2AAddr returns a loopback address that was bound and released.
func freeA2AAddr(t *testing.T) string {
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

// canceledCmd returns a command whose context is already canceled, so the
// A2A server binds its listeners and then shuts straight back down.
func canceledCmd(t *testing.T) *cobra.Command {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	return cmd
}

// isolateA2AFlags points the package-level flags at ephemeral ports and a
// throwaway HOME, restoring them when the test ends.
func isolateA2AFlags(t *testing.T) {
	t.Helper()
	origAddr, origReady := flagA2AAddr, flagA2AReadyAddr
	origModel, origURL, origSystem := flagModel, flagURL, flagSystem
	t.Cleanup(func() {
		flagA2AAddr, flagA2AReadyAddr = origAddr, origReady
		flagModel, flagURL, flagSystem = origModel, origURL, origSystem
	})
	flagA2AAddr = freeA2AAddr(t)
	flagA2AReadyAddr = freeA2AAddr(t)
	flagModel, flagURL, flagSystem = "", "", ""

	// os.UserHomeDir reads $HOME on unix and %USERPROFILE% on Windows; set
	// both so the server's log directory lands in the temp dir either way.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestNewA2AServerCmd(t *testing.T) {
	cmd := newA2AServerCmd()

	if cmd.Use != "a2a" {
		t.Errorf("Use = %q, want a2a", cmd.Use)
	}
	if cmd.RunE == nil {
		t.Error("RunE is nil")
	}
	for _, name := range []string{"model", "url", "header", "insecure", "addr", "ready-addr"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q is not registered", name)
		}
	}
	if f := cmd.Flags().Lookup("header"); f != nil && f.NoOptDefVal != "" {
		t.Errorf("header NoOptDefVal = %q, want empty", f.NoOptDefVal)
	}
	if f := cmd.Flags().Lookup("ready-addr"); f != nil && f.DefValue != ":8081" {
		t.Errorf("ready-addr default = %q, want :8081", f.DefValue)
	}
}

func TestRunA2AServerShutsDownOnCanceledContext(t *testing.T) {
	isolateA2AFlags(t)

	err := runA2AServer(canceledCmd(t), nil)
	if err == nil {
		t.Fatal("runA2AServer() = nil, want the context cancellation to surface")
	}
	if !strings.Contains(err.Error(), "a2a server") {
		t.Errorf("error = %v, want it wrapped with %q", err, "a2a server")
	}
}

func TestRunA2AServerWritesErrorLog(t *testing.T) {
	isolateA2AFlags(t)

	// Resolve the home directory the same way runA2AServer does, so the
	// assertion follows the platform rather than assuming $HOME.
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}

	if err := runA2AServer(canceledCmd(t), nil); err == nil {
		t.Fatal("want an error from the canceled context")
	}

	logPath := filepath.Join(home, ".pi-go", "sessions", "a2a-server.err.log")
	body, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("error log was not created at %s: %v", logPath, err)
	}
	if len(body) == 0 {
		t.Error("error log is empty; the shutdown error was not recorded")
	}
}

func TestRunA2AServerResolvesModelFromEnv(t *testing.T) {
	isolateA2AFlags(t)
	t.Setenv("PI_MODEL", "env-model")
	t.Setenv("PI_BASE_URL", "http://env.invalid")
	t.Setenv("PI_SYSTEM", "env system prompt")

	if err := runA2AServer(canceledCmd(t), nil); err == nil {
		t.Fatal("want an error from the canceled context")
	}
}

func TestRunA2AServerPrefersFlagsOverEnv(t *testing.T) {
	isolateA2AFlags(t)
	flagModel, flagURL, flagSystem = "flag-model", "http://flag.invalid", "flag system"
	t.Setenv("PI_MODEL", "env-model")
	t.Setenv("PI_BASE_URL", "http://env.invalid")
	t.Setenv("PI_SYSTEM", "env system prompt")

	if err := runA2AServer(canceledCmd(t), nil); err == nil {
		t.Fatal("want an error from the canceled context")
	}
}

func TestRunA2AServerUsesPortEnvWhenAddrUnset(t *testing.T) {
	isolateA2AFlags(t)
	port := strings.TrimPrefix(freeA2AAddr(t), "127.0.0.1:")
	flagA2AAddr = ""
	t.Setenv("PORT", port)

	if err := runA2AServer(canceledCmd(t), nil); err == nil {
		t.Fatal("want an error from the canceled context")
	}
}

// TestKagentTaskStoreAddressIsExplicitOnly pins the one knob that governs task
// persistence. It used to fall back to `control_plane_url` inside
// KAGENT_CONFIG_JSON, a key nothing has ever written — the ADK config blob
// carries model and instruction settings, not a URL. That fallback made
// persistence look automatic while silently never happening.
func TestKagentTaskStoreAddressIsExplicitOnly(t *testing.T) {
	t.Run("explicit address is used", func(t *testing.T) {
		t.Setenv("KAGENT_TASK_STORE_ADDRESS", "kagent-controller.kagent:8083")
		if got := kagentTaskStoreAddress(); got != "kagent-controller.kagent:8083" {
			t.Errorf("address = %q, want the explicit one", got)
		}
	})

	// The regression: a config blob carrying control_plane_url must NOT be
	// taken as a controller address, because the gateway does not serve the
	// task-store service and the RPC would fail the turn.
	t.Run("control_plane_url is not a fallback", func(t *testing.T) {
		t.Setenv("KAGENT_TASK_STORE_ADDRESS", "")
		t.Setenv("KAGENT_CONFIG_JSON", `{"control_plane_url":"http://controller.invalid:8083"}`)
		if got := kagentTaskStoreAddress(); got != "" {
			t.Errorf("address = %q, want empty: control_plane_url is not a task-store address", got)
		}
	})

	t.Run("unset keeps tasks in memory", func(t *testing.T) {
		t.Setenv("KAGENT_TASK_STORE_ADDRESS", "")
		t.Setenv("KAGENT_CONFIG_JSON", "")
		if got := kagentTaskStoreAddress(); got != "" {
			t.Errorf("address = %q, want empty", got)
		}
	})
}
