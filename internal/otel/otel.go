// Package otel provides lightweight OpenTelemetry setup for pi-go,
// driven entirely by environment variables sourced from ~/.pi-go/.env
// so secrets never appear in the process environment.
//
// The following env vars are consumed:
//
//	OTEL_SERVICE_NAME        defaults to "pi-go"
//	OTEL_EXPORTER_OTLP_ENDPOINT  collector endpoint (e.g. https://collector:4317)
//	OTEL_EXPORTER_OTLP_PROTOCOL  "grpc" or "http" (default)
//	OTEL_TRACES_EXPORTER    "otlp" (default), "console", or "none"
//
// If no exporter is configured, tracing is a no-op (tracer returns no-op spans).
package otel

import (
	"context"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otlptrace "go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.27.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracerProvider *sdktrace.TracerProvider
	initOnce       sync.Once

	// exportErrCount and lastExportErr capture what the OTel SDK would
	// otherwise print to stderr. See captureExportError.
	exportErrCount atomic.Int64
	lastExportErr  atomic.Pointer[string]
)

// captureExportError is the global OTel ErrorHandler. The SDK's default
// handler writes to stderr with a plain log.Logger, so an unreachable
// collector paints lines like
//
//	2026/08/05 09:54:16 traces export: exporter export timeout: rpc error: ...
//
// straight over whatever the TUI had drawn. Every batch retry repeats it.
// pi owns the screen, so these are recorded here instead of printed; read
// them back with ExportErrors when diagnosing a silent collector.
func captureExportError(err error) {
	if err == nil {
		return
	}
	exportErrCount.Add(1)
	msg := err.Error()
	lastExportErr.Store(&msg)
}

// ExportErrors returns how many telemetry export errors have been swallowed
// since start, and the most recent one ("" if there has been none).
func ExportErrors() (count int64, last string) {
	if p := lastExportErr.Load(); p != nil {
		last = *p
	}
	return exportErrCount.Load(), last
}

// ResetForTest resets the lazy-initialized tracer provider. It is intended
// for tests that need to re-read the OTEL configuration from the current
// ~/.pi-go/.env or process environment, e.g. when the test process has
// already triggered init once with a different configuration. Production
// code should never call this.
func ResetForTest() {
	if tracerProvider != nil {
		_ = tracerProvider.Shutdown(context.Background())
	}
	tracerProvider = nil
	initOnce = sync.Once{}
}

// Tracer returns a named tracer for the acp-server package.
// The underlying TracerProvider is initialized once from the .env source.
func Tracer(name string) trace.Tracer {
	initProvider()
	return otel.Tracer(name)
}

// initProvider reads OTEL_* vars from ~/.pi-go/.env and builds a
// TracerProvider. It is safe to call multiple times; the provider is set
// exactly once.
func initProvider() {
	initOnce.Do(func() {
		// Silence the OTel SDK's default stderr logger immediately so no internal
		// SDK messages (URL parse errors, etc.) ever reach the user's terminal.
		// The SDK default is stdr→os.Stderr which pollutes ACP/TUI stdout streams.
		otel.SetLogger(logr.Discard())
		// Same reasoning for the error handler: batch export failures reach
		// the terminal through otel.Handle, not through the logger.
		otel.SetErrorHandler(otel.ErrorHandlerFunc(captureExportError))

		ctx := context.Background()

		serviceName := envOr(".env", "OTEL_SERVICE_NAME", "pi-go")
		exporter := envOr(".env", "OTEL_TRACES_EXPORTER", "otlp")

		// Discover resource: combine service name with environment.
		res, _ := resource.New(ctx,
			resource.WithAttributes(
				semconv.ServiceName(serviceName),
				semconv.ServiceVersion("dev"),
			),
		)

		var tp *sdktrace.TracerProvider

		switch exporter {
		case "console":
			// TODO: console exporter – for now fall through to no-op.
		case "none", "":
			// Explicit opt-out; create a no-op provider.
			tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
		default: // "otlp" or any other value
			protocol := envOr(".env", "OTEL_EXPORTER_OTLP_PROTOCOL", "http")
			endpoint := envOr(".env", "OTEL_EXPORTER_OTLP_ENDPOINT", "")

			var exp *otlptrace.Exporter
			var err error
			if protocol == "grpc" {
				exp, err = otlptracegrpc.New(ctx,
					otlptracegrpc.WithEndpointURL(normalizeEndpointURL(endpoint, protocol)),
				)
			} else {
				// OTel Go v1.45+ no longer appends /v1/traces for pathless
				// WithEndpointURL values; add it so OTEL_EXPORTER_OTLP_ENDPOINT
				// behaves like the spec (base URL → /v1/traces).
				exp, err = otlptracehttp.New(ctx,
					otlptracehttp.WithEndpointURL(httpTraceEndpointURL(endpoint)),
				)
			}
			if err == nil {
				tp = sdktrace.NewTracerProvider(
					sdktrace.WithBatcher(exp),
					sdktrace.WithResource(res),
				)
			} else {
				// Fall back to a no-op provider, silently. Nothing here may write to
				// the terminal: pi owns the screen, and a stray line painted straight
				// onto it (this used to go to /dev/tty, which no redirection can
				// catch) lands in the middle of whatever the TUI had drawn there. It
				// is the same reason the SDK's own logger is discarded above.
				tp = sdktrace.NewTracerProvider(sdktrace.WithResource(res))
			}
		}

		if tp != nil {
			otel.SetTracerProvider(tp)
			otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
				propagation.TraceContext{},
				propagation.Baggage{},
			))
			tracerProvider = tp
		}
	})
}

// IsEnabled returns true when OTEL tracing is active (exporter is set and not "none"/"").
func IsEnabled() bool {
	exporter := envOr(".env", "OTEL_TRACES_EXPORTER", "")
	// If exporter is explicitly set to something other than "none" or empty, tracing is active.
	return exporter != "" && exporter != "none"
}

// IsAvailable checks whether the configured OTLP endpoint is reachable.
// Returns true if the port is open, false otherwise. Returns false if
// exporter is set to "none" or empty (tracing is inactive anyway).
func IsAvailable() bool {
	exporter := envOr(".env", "OTEL_TRACES_EXPORTER", "")
	if exporter == "" || exporter == "none" {
		return false
	}
	protocol := envOr(".env", "OTEL_EXPORTER_OTLP_PROTOCOL", "http")
	endpoint := envOr(".env", "OTEL_EXPORTER_OTLP_ENDPOINT", "")

	addr := dialAddress(endpoint, protocol)
	if addr == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// dialAddress resolves the configured endpoint to a host:port to probe. It
// reuses normalizeEndpointURL so the same defaults apply as when the exporter
// is built, and it honors the endpoint's host: probing localhost regardless of
// where the collector actually lives reported every remote collector as
// unavailable — and, worse, reported one as available whenever anything local
// happened to listen on the same port. Returns "" when the endpoint cannot be
// parsed.
func dialAddress(endpoint, protocol string) string {
	u, err := url.Parse(normalizeEndpointURL(endpoint, protocol))
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Port() != "" {
		return u.Host
	}
	// No explicit port: fall back to the scheme's default, then to the OTLP
	// default for the configured protocol.
	switch {
	case u.Scheme == "https":
		return net.JoinHostPort(u.Hostname(), "443")
	case protocol == "grpc":
		return net.JoinHostPort(u.Hostname(), "4317")
	default:
		return net.JoinHostPort(u.Hostname(), "4318")
	}
}

// Shutdown flushes and shuts down the global TracerProvider.
// Call this at program exit.
func Shutdown(ctx context.Context) error {
	if tracerProvider != nil {
		return tracerProvider.Shutdown(ctx)
	}
	return nil
}

// envOr reads key from ~/.pi-go/.env, falling back to process env.
func envOr(dotEnvPath, key, fallback string) string {
	v := loadEnvFromDotEnv(dotEnvPath, key)
	if v != "" {
		return v
	}
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// AttributeString returns a string attribute key-value pair.
func AttributeString(key string, value string) attribute.KeyValue {
	return attribute.String(key, value)
}

// AttributeBool returns a bool attribute key-value pair.
func AttributeBool(key string, value bool) attribute.KeyValue {
	return attribute.Bool(key, value)
}

// AttributeInt returns an int attribute key-value pair.
func AttributeInt(key string, value int) attribute.KeyValue {
	return attribute.Int(key, value)
}

// httpTraceEndpointURL resolves the OTLP/HTTP traces export URL. When the
// configured endpoint has no path (or only "/"), /v1/traces is appended so a
// base URL like http://localhost:4318 reaches the collector's trace endpoint.
func httpTraceEndpointURL(endpoint string) string {
	endpointURL := normalizeEndpointURL(endpoint, "http")
	u, err := url.Parse(endpointURL)
	if err != nil || u.Path == "" || u.Path == "/" {
		return strings.TrimSuffix(endpointURL, "/") + "/v1/traces"
	}
	return endpointURL
}

// normalizeEndpointURL ensures the endpoint has an http:// scheme so the OTel
// SDK can parse it as a URL. Raw host:port values (as written in .env for gRPC)
// are prefixed with "http://"; values that already carry a scheme are returned
// unchanged.
func normalizeEndpointURL(endpoint, protocol string) string {
	if endpoint == "" {
		if protocol == "grpc" {
			return "http://localhost:4317"
		}
		return "http://localhost:4318"
	}
	if !strings.Contains(endpoint, "://") {
		return "http://" + endpoint
	}
	return endpoint
}

// loadEnvFromDotEnv reads a single key from dotEnvPath. If dotEnvPath is
// absolute it is used as-is; otherwise it is resolved as a filename within
// ~/.pi-go/ (e.g. ".env" → ~/.pi-go/.env).
func loadEnvFromDotEnv(dotEnvPath, key string) string {
	// filepath.IsAbs, not a leading-slash check: a Windows absolute path starts
	// with a drive letter, and treating it as relative resolved it under
	// ~/.pi-go/ and silently read nothing.
	var path string
	if filepath.IsAbs(dotEnvPath) {
		path = dotEnvPath
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(home, ".pi-go", dotEnvPath)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, val, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == key {
			val = strings.TrimSpace(val)
			// Strip matching surrounding quotes (handles .env files with quoted values).
			if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
				val = val[1 : len(val)-1]
			}
			return val
		}
	}
	return ""
}
