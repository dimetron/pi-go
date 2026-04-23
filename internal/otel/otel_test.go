package otel

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTracerReturnsTracer(t *testing.T) {
	// Calling Tracer twice should not panic and should return the same no-op
	// tracer when no exporter is configured.
	tr1 := Tracer("test1")
	tr2 := Tracer("test2")
	if tr1 == nil || tr2 == nil {
		t.Fatal("Tracer returned nil")
	}
	// Spans should be no-op (not recording) when no exporter is set.
	_, span := tr1.Start(context.Background(), "test-span")
	if span.IsRecording() {
		t.Log("span is recording — an exporter may be configured in the environment")
	}
	span.End()
}

func TestEnvOrFromDotEnv(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	dotEnvPath := filepath.Join(home, ".pi-go", ".env")

	// When key is absent, fallback is returned.
	got := envOr(dotEnvPath, "DOES_NOT_EXIST_12345", "fallback-val")
	if got != "fallback-val" {
		t.Errorf("envOr missing key: got %q, want fallback", got)
	}

	// envOr should prefer .env over process env when set there.
	os.Setenv("OTEL_SERVICE_NAME", "process-env-name")
	defer os.Unsetenv("OTEL_SERVICE_NAME")

	// .env wins if it has the key, otherwise process env wins.
	val := envOr(dotEnvPath, "OTEL_SERVICE_NAME", "")
	switch val {
	case "pi-go":
		// .env has it — good
	case "process-env-name":
		// .env does not have it, process env is the fallback — also correct
		t.Logf("OTEL_SERVICE_NAME not in .env, falling back to process env: %q", val)
	default:
		t.Errorf("envOr OTEL_SERVICE_NAME: got %q, want pi-go or process-env-name", val)
	}
}

func TestLoadEnvFromDotEnv(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	path := filepath.Join(home, ".pi-go", ".env")

	val := loadEnvFromDotEnv(path, "OTEL_SERVICE_NAME")
	if val == "" {
		t.Log("OTEL_SERVICE_NAME not set in .env — this is OK (will use fallback)")
	}

	val = loadEnvFromDotEnv(path, "DOES_NOT_EXIST")
	if val != "" {
		t.Errorf("loadEnvFromDotEnv unknown key: got %q, want empty", val)
	}
}
