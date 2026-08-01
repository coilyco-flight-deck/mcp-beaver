package telemetry

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

var telemetryEnv = []string{
	"OTEL_SDK_DISABLED",
	"OTEL_TRACES_EXPORTER",
	"OTEL_METRICS_EXPORTER",
	"OTEL_EXPORTER_OTLP_ENDPOINT",
	"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
	"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT",
	"OTEL_EXPORTER_OTLP_PROTOCOL",
	"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
	"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL",
	"OTEL_SERVICE_NAME",
	"OTEL_RESOURCE_ATTRIBUTES",
}

func clearTelemetryEnv(t *testing.T) {
	t.Helper()
	for _, name := range telemetryEnv {
		t.Setenv(name, "")
	}
}

func TestSetupIsNoopWithoutExplicitExport(t *testing.T) {
	clearTelemetryEnv(t)
	runtime, err := Setup(context.Background(), "resolved-server")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Enabled() {
		t.Fatal("unset telemetry environment enabled an exporter")
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSetupHonorsDisabledAndNoneSelectors(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{name: "sdk disabled", env: map[string]string{"OTEL_SDK_DISABLED": "true", "OTEL_TRACES_EXPORTER": "invalid"}},
		{name: "none selectors", env: map[string]string{"OTEL_TRACES_EXPORTER": "none", "OTEL_METRICS_EXPORTER": "none"}},
		{name: "none beats endpoints", env: map[string]string{
			"OTEL_TRACES_EXPORTER": "none", "OTEL_METRICS_EXPORTER": "none", "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearTelemetryEnv(t)
			for name, value := range test.env {
				t.Setenv(name, value)
			}
			runtime, err := Setup(context.Background(), "resolved-server")
			if err != nil {
				t.Fatal(err)
			}
			if runtime.Enabled() {
				t.Fatal("disabled telemetry installed an active provider")
			}
		})
	}
}

func TestSetupRejectsExplicitInvalidConfiguration(t *testing.T) {
	t.Run("disabled flag", func(t *testing.T) {
		clearTelemetryEnv(t)
		t.Setenv("OTEL_SDK_DISABLED", "sometimes")
		_, err := Setup(context.Background(), "resolved-server")
		if err == nil || !strings.Contains(err.Error(), "OTEL_SDK_DISABLED") {
			t.Fatalf("error = %v, want useful disabled flag error", err)
		}
	})
	t.Run("trace selector", func(t *testing.T) {
		clearTelemetryEnv(t)
		t.Setenv("OTEL_TRACES_EXPORTER", "not-an-exporter")
		_, err := Setup(context.Background(), "resolved-server")
		if err == nil || !strings.Contains(err.Error(), "OpenTelemetry traces") {
			t.Fatalf("error = %v, want useful trace exporter error", err)
		}
	})
	t.Run("protocol without exporter", func(t *testing.T) {
		clearTelemetryEnv(t)
		t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "carrier-pigeon")
		_, err := Setup(context.Background(), "resolved-server")
		if err == nil || !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_PROTOCOL") {
			t.Fatalf("error = %v, want useful protocol error", err)
		}
	})
	t.Run("resource without exporter", func(t *testing.T) {
		clearTelemetryEnv(t)
		t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "not-valid")
		_, err := Setup(context.Background(), "resolved-server")
		if err == nil || !strings.Contains(err.Error(), "OpenTelemetry resource") {
			t.Fatalf("error = %v, want useful resource error", err)
		}
	})
}

func TestConfiguredResourceIdentity(t *testing.T) {
	clearTelemetryEnv(t)
	res, err := configuredResource(context.Background(), "resolved-server")
	if err != nil {
		t.Fatal(err)
	}
	if got := resourceString(res.Set(), "service.name"); got != "resolved-server" {
		t.Fatalf("default service.name = %q, want resolved-server", got)
	}

	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=resource-name,deployment.environment.name=test")
	t.Setenv("OTEL_SERVICE_NAME", "explicit-name")
	res, err = configuredResource(context.Background(), "resolved-server")
	if err != nil {
		t.Fatal(err)
	}
	if got := resourceString(res.Set(), "service.name"); got != "explicit-name" {
		t.Fatalf("overridden service.name = %q, want explicit-name", got)
	}
	if got := resourceString(res.Set(), "deployment.environment.name"); got != "test" {
		t.Fatalf("resource attribute = %q, want test", got)
	}
}

func resourceString(set *attribute.Set, key string) string {
	value, ok := set.Value(attribute.Key(key))
	if !ok {
		return ""
	}
	return value.AsString()
}

func TestShutdownFlushesAndHonorsDeadline(t *testing.T) {
	t.Run("flush", func(t *testing.T) {
		exporter := &preservingExporter{}
		provider := sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(time.Hour)),
		)
		_, span := provider.Tracer("test").Start(context.Background(), "completed")
		span.End()
		runtime := &Runtime{enabled: true, shutdowns: []shutdownFunc{provider.Shutdown}}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := runtime.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		if got := exporter.count(); got != 1 {
			t.Fatalf("exported spans = %d, want 1 flushed span", got)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		runtime := &Runtime{enabled: true, shutdowns: []shutdownFunc{
			func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		started := time.Now()
		err := runtime.Shutdown(ctx)
		if err == nil || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
			t.Fatalf("shutdown error = %v, want deadline", err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("shutdown took %s despite bounded context", elapsed)
		}
	})
}

type preservingExporter struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (e *preservingExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.spans = append(e.spans, spans...)
	return nil
}

func (*preservingExporter) Shutdown(context.Context) error { return nil }

func (e *preservingExporter) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.spans)
}
