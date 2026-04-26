// Package otel provides lightweight OpenTelemetry setup for pi-go,
// driven entirely by environment variables sourced from ~/.pi-go/.env
// so secrets never appear in the process environment.
//
// The following env vars are consumed:
//
//	EL_SERVICE_NAME        defaults to "pi-go"
//	OTEL_EXPORTER_OTLP_ENDPOINT  collector endpoint (e.g. https://collector:4317)
//	OTEL_EXPORTER_OTLP_PROTOCOL  "grpc" or "http" (default)
//	OTEL_TRACES_EXPORTER    "otlp" (default), "console", or "none"
//
// If no exporter is configured, tracing is a no-op (tracer returns no-op spans).
package otel

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
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
)

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
			endpointURL := normalizeEndpointURL(endpoint, protocol)

			var exp *otlptrace.Exporter
			var err error
			if protocol == "grpc" {
				exp, err = otlptracegrpc.New(ctx,
					otlptracegrpc.WithEndpointURL(endpointURL),
				)
			} else {
				exp, err = otlptracehttp.New(ctx,
					otlptracehttp.WithEndpointURL(endpointURL),
				)
			}
			if err == nil {
				tp = sdktrace.NewTracerProvider(
					sdktrace.WithBatcher(exp),
					sdktrace.WithResource(res),
				)
			} else {
				// Fallback: no-op provider.
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
	endpoint := envOr(".env", "OTEL_EXPORTER_OTLP_ENDPOINT", "")
	port := 4317
	if strings.Contains(endpoint, ":") {
		if p, err := strconv.Atoi(strings.Split(endpoint, ":")[1]); err == nil {
			port = p
		}
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
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
	var path string
	if strings.HasPrefix(dotEnvPath, "/") {
		path = dotEnvPath
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = home + "/.pi-go/" + dotEnvPath
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
