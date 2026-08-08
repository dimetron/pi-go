package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"google.golang.org/adk/v2/session"
)

func TestCaptureHostEnv_ReportsPlausibleNumbers(t *testing.T) {
	env := captureHostEnv(t.TempDir())

	if env.OS != runtime.GOOS {
		t.Errorf("OS = %q, want %q", env.OS, runtime.GOOS)
	}
	if env.Arch != runtime.GOARCH {
		t.Errorf("Arch = %q, want %q", env.Arch, runtime.GOARCH)
	}
	if env.CPUs <= 0 {
		t.Errorf("CPUs = %d, want a positive count", env.CPUs)
	}

	// Disk is reported on every platform this is tested on.
	if env.DiskTotalBytes == 0 {
		t.Error("DiskTotalBytes = 0; the temp dir's filesystem should report a size")
	}
	if env.DiskFreeBytes > env.DiskTotalBytes {
		t.Errorf("DiskFreeBytes %d exceeds DiskTotalBytes %d", env.DiskFreeBytes, env.DiskTotalBytes)
	}

	switch runtime.GOOS {
	case "darwin", "linux":
		if env.TotalMemoryBytes == 0 {
			t.Error("TotalMemoryBytes = 0 on a platform that reports it")
		}
		if env.AvailableMemoryBytes > env.TotalMemoryBytes {
			t.Errorf("AvailableMemoryBytes %d exceeds TotalMemoryBytes %d",
				env.AvailableMemoryBytes, env.TotalMemoryBytes)
		}
	}
}

func TestCaptureHostEnv_UnreadablePathStillReturnsRuntimeFacts(t *testing.T) {
	// A bad path must degrade to zeroed disk figures, not fail — this runs on
	// the session-creation path and must never be the reason a session cannot
	// be created.
	env := captureHostEnv("/definitely/does/not/exist/anywhere")

	if env.CPUs <= 0 {
		t.Errorf("CPUs = %d, want the runtime facts regardless", env.CPUs)
	}
	if env.DiskTotalBytes != 0 || env.DiskFreeBytes != 0 {
		t.Errorf("disk figures should be zero for an unreadable path, got %d/%d",
			env.DiskFreeBytes, env.DiskTotalBytes)
	}
}

func TestCreate_PersistsHostEnvAndModel(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: "pi-go", UserID: "local",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := resp.Session.ID()

	raw, err := os.ReadFile(filepath.Join(svc.baseDir, id, "meta.json"))
	if err != nil {
		t.Fatalf("reading meta.json: %v", err)
	}

	var meta Meta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("parsing meta.json: %v", err)
	}

	if meta.Host == nil {
		t.Fatal("meta.json carries no host snapshot")
	}
	if meta.Host.CPUs <= 0 {
		t.Errorf("host.cpus = %d, want a positive count", meta.Host.CPUs)
	}
	if meta.Host.OS != runtime.GOOS {
		t.Errorf("host.os = %q, want %q", meta.Host.OS, runtime.GOOS)
	}
	// The whole point is troubleshooting a kill after the fact, so the two
	// resource numbers have to survive the round trip.
	if meta.Host.DiskTotalBytes == 0 {
		t.Error("host.diskTotalBytes did not survive serialization")
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if meta.Host.TotalMemoryBytes == 0 {
			t.Error("host.totalMemoryBytes did not survive serialization")
		}
	}
}

func TestSetSessionProvider_RecordsBackend(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: "pi-go", UserID: "local",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := resp.Session.ID()

	if err := svc.SetSessionModel(id, "qwen2.5:latest"); err != nil {
		t.Fatalf("SetSessionModel: %v", err)
	}
	if err := svc.SetSessionProvider(id, "ollama", "http://localhost:11434"); err != nil {
		t.Fatalf("SetSessionProvider: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(svc.baseDir, id, "meta.json"))
	if err != nil {
		t.Fatalf("reading meta.json: %v", err)
	}
	var meta Meta
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("parsing meta.json: %v", err)
	}

	if meta.Model != "qwen2.5:latest" {
		t.Errorf("model = %q, want %q", meta.Model, "qwen2.5:latest")
	}
	// The same model name means different things per backend, so the provider
	// is what makes the record unambiguous.
	if meta.Provider != "ollama" {
		t.Errorf("provider = %q, want %q", meta.Provider, "ollama")
	}
	if meta.BaseURL != "http://localhost:11434" {
		t.Errorf("baseURL = %q, want the endpoint", meta.BaseURL)
	}
}

func TestSetSessionProvider_EmptyValuesStayOutOfMeta(t *testing.T) {
	svc := newTestService(t)

	resp, err := svc.Create(context.Background(), &session.CreateRequest{
		AppName: "pi-go", UserID: "local",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := resp.Session.ID()

	if err := svc.SetSessionProvider(id, "", ""); err != nil {
		t.Fatalf("SetSessionProvider: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(svc.baseDir, id, "meta.json"))
	if err != nil {
		t.Fatalf("reading meta.json: %v", err)
	}
	var generic map[string]any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("parsing meta.json: %v", err)
	}
	for _, k := range []string{"provider", "baseURL"} {
		if _, present := generic[k]; present {
			t.Errorf("empty %q was written to meta.json; omitempty should drop it", k)
		}
	}
}
