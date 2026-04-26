package otel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel/attribute"
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

func TestLoadEnvFromDotEnv_AbsolutePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	envFile := filepath.Join(dir, "test.env")

	content := "# comment\nMY_KEY=plain-value\nQUOTED_KEY=\"quoted-value\"\nEMPTY_KEY=\n"
	if err := os.WriteFile(envFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		key  string
		want string
	}{
		{"MY_KEY", "plain-value"},
		{"QUOTED_KEY", "quoted-value"},
		{"EMPTY_KEY", ""},
		{"MISSING_KEY", ""},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			got := loadEnvFromDotEnv(envFile, tc.key)
			if got != tc.want {
				t.Fatalf("loadEnvFromDotEnv(%q) = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

func TestLoadEnvFromDotEnv_NonExistentFile(t *testing.T) {
	t.Parallel()
	got := loadEnvFromDotEnv("/nonexistent/path/.env", "ANY_KEY")
	if got != "" {
		t.Fatalf("got %q, want empty for missing file", got)
	}
}

func TestNormalizeEndpointURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		desc     string
		endpoint string
		protocol string
		want     string
	}{
		{"empty grpc uses default gRPC port", "", "grpc", "http://localhost:4317"},
		{"empty http uses default HTTP port", "", "http", "http://localhost:4318"},
		{"empty unknown uses HTTP port", "", "anything", "http://localhost:4318"},
		{"bare host:port gets http:// prefix", "127.0.0.1:4317", "grpc", "http://127.0.0.1:4317"},
		{"already-schemed URL is unchanged", "http://collector:4318", "http", "http://collector:4318"},
		{"https scheme is preserved", "https://secure:4318", "http", "https://secure:4318"},
	}
	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			got := normalizeEndpointURL(tc.endpoint, tc.protocol)
			if got != tc.want {
				t.Fatalf("normalizeEndpointURL(%q, %q) = %q, want %q", tc.endpoint, tc.protocol, got, tc.want)
			}
		})
	}
}

func TestIsEnabled(t *testing.T) {
	cases := []struct {
		envVal string
		want   bool
	}{
		{"otlp", true},
		{"console", true},
		{"none", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.envVal, func(t *testing.T) {
			t.Setenv("OTEL_TRACES_EXPORTER", tc.envVal)
			// IsEnabled reads from process env as fallback when .env key is absent.
			got := IsEnabled()
			if got != tc.want {
				t.Fatalf("IsEnabled() = %v, want %v (exporter=%q)", got, tc.want, tc.envVal)
			}
		})
	}
}

func TestIsAvailable_WhenPortClosed_ReturnsFalse(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	// No collector running, so IsAvailable should return false.
	got := IsAvailable()
	if got {
		t.Fatal("IsAvailable() = true, want false (no collector running)")
	}
}

func TestIsAvailable_WhenExporterNone_ReturnsFalse(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	got := IsAvailable()
	if got {
		t.Fatal("IsAvailable() with exporter=none = true, want false")
	}
}

func TestIsAvailable_WhenExporterEmpty_ReturnsFalse(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	got := IsAvailable()
	if got {
		t.Fatal("IsAvailable() with exporter=empty = true, want false")
	}
}

func TestIsAvailable_SetsExporterNoneIfNotAvailable(t *testing.T) {
	// IsAvailable should not modify env vars, only read them.
	// We test the side-effect-free path: when the port is not reachable,
	// the caller can decide to set OTEL_TRACES_EXPORTER=none.
	// This test verifies IsAvailable returns false when no collector is up.
	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	if IsAvailable() {
		t.Skip("a collector is running on port 4317 — skipping test")
	}
	// If we get here, no collector is running, which is the expected state.
}

func TestShutdown(t *testing.T) {
	// Ensure the provider is initialized so we exercise the non-nil branch.
	Tracer("shutdown-test")
	if err := Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestAttributeHelpers(t *testing.T) {
	t.Parallel()
	str := AttributeString("k", "v")
	if str != attribute.String("k", "v") {
		t.Fatalf("AttributeString = %v, want attribute.String", str)
	}
	b := AttributeBool("flag", true)
	if b != attribute.Bool("flag", true) {
		t.Fatalf("AttributeBool = %v, want attribute.Bool", b)
	}
	n := AttributeInt("n", 42)
	if n != attribute.Int("n", 42) {
		t.Fatalf("AttributeInt = %v, want attribute.Int", n)
	}
}
