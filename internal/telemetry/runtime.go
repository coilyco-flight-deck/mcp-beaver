// Package telemetry owns mcp-beaver's opt-in OpenTelemetry provider lifecycle.
// Instrumentation stays inert unless an exporter selector or OTLP endpoint is
// explicit, so an unset environment never inherits autoexport's OTLP default.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

const (
	tracesExporterEnv  = "OTEL_TRACES_EXPORTER"
	metricsExporterEnv = "OTEL_METRICS_EXPORTER"
	globalEndpointEnv  = "OTEL_EXPORTER_OTLP_ENDPOINT"
	tracesEndpointEnv  = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	metricsEndpointEnv = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
)

type shutdownFunc func(context.Context) error

// Runtime owns the providers installed by Setup.
type Runtime struct {
	enabled      bool
	shutdowns    []shutdownFunc
	shutdownOnce sync.Once
	shutdownErr  error
}

// Setup installs providers configured through standard OTEL_* environment
// variables. Export is opt-in: a selector or OTLP endpoint must be explicit.
func Setup(ctx context.Context, defaultServiceName string) (*Runtime, error) {
	disabled, err := sdkDisabled()
	if err != nil {
		return nil, err
	}
	propagator := W3CPropagator()
	otel.SetTextMapPropagator(propagator)
	if disabled {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		return &Runtime{}, nil
	}
	if err := validateProtocols(); err != nil {
		return nil, err
	}

	tracesEnabled := signalEnabled(tracesExporterEnv, tracesEndpointEnv)
	metricsEnabled := signalEnabled(metricsExporterEnv, metricsEndpointEnv)
	if !tracesEnabled && !metricsEnabled {
		if strings.TrimSpace(os.Getenv("OTEL_RESOURCE_ATTRIBUTES")) != "" {
			if _, err := configuredResource(ctx, defaultServiceName); err != nil {
				return nil, err
			}
		}
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		return &Runtime{}, nil
	}

	res, err := configuredResource(ctx, defaultServiceName)
	if err != nil {
		return nil, err
	}

	runtime := &Runtime{enabled: true}
	var tracerProvider *sdktrace.TracerProvider
	if tracesEnabled {
		exporter, err := autoexport.NewSpanExporter(ctx)
		if err != nil {
			return nil, fmt.Errorf("configure OpenTelemetry traces: %w", err)
		}
		tracerProvider = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(res),
		)
		runtime.shutdowns = append(runtime.shutdowns, tracerProvider.Shutdown)
	}

	var meterProvider *sdkmetric.MeterProvider
	if metricsEnabled {
		reader, err := autoexport.NewMetricReader(ctx)
		if err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = runtime.shutdown(cleanupCtx)
			return nil, fmt.Errorf("configure OpenTelemetry metrics: %w", err)
		}
		meterProvider = sdkmetric.NewMeterProvider(
			sdkmetric.WithReader(reader),
			sdkmetric.WithResource(res),
		)
		runtime.shutdowns = append([]shutdownFunc{meterProvider.Shutdown}, runtime.shutdowns...)
	}

	if tracerProvider != nil {
		otel.SetTracerProvider(tracerProvider)
	} else {
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
	}
	if meterProvider != nil {
		otel.SetMeterProvider(meterProvider)
	} else {
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
	}
	return runtime, nil
}

func validateProtocols() error {
	for _, name := range []string{
		"OTEL_EXPORTER_OTLP_PROTOCOL",
		"OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
		"OTEL_EXPORTER_OTLP_METRICS_PROTOCOL",
	} {
		value := strings.TrimSpace(os.Getenv(name))
		if value != "" && value != "grpc" && value != "http/protobuf" {
			return fmt.Errorf("configure OpenTelemetry: %s must be grpc or http/protobuf", name)
		}
	}
	return nil
}

// W3CPropagator is the MCP convention's trace context and baggage propagator.
func W3CPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	)
}

// Enabled reports whether either traces or metrics has an active provider.
func (r *Runtime) Enabled() bool {
	return r != nil && r.enabled
}

// Shutdown flushes and closes every active provider. The caller supplies the
// bounded context that caps the combined shutdown.
func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.shutdownOnce.Do(func() {
		r.shutdownErr = r.shutdown(ctx)
	})
	return r.shutdownErr
}

func (r *Runtime) shutdown(ctx context.Context) error {
	var errs []error
	for _, shutdown := range r.shutdowns {
		if err := shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func sdkDisabled() (bool, error) {
	raw, ok := os.LookupEnv("OTEL_SDK_DISABLED")
	if !ok || strings.TrimSpace(raw) == "" {
		return false, nil
	}
	disabled, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false, fmt.Errorf("configure OpenTelemetry: OTEL_SDK_DISABLED must be a boolean: %w", err)
	}
	return disabled, nil
}

func signalEnabled(selectorEnv, signalEndpointEnv string) bool {
	selector := strings.TrimSpace(os.Getenv(selectorEnv))
	if strings.EqualFold(selector, "none") {
		return false
	}
	return selector != "" || strings.TrimSpace(os.Getenv(signalEndpointEnv)) != "" || strings.TrimSpace(os.Getenv(globalEndpointEnv)) != ""
}

func configuredResource(ctx context.Context, defaultServiceName string) (*resource.Resource, error) {
	defaultServiceName = strings.TrimSpace(defaultServiceName)
	if defaultServiceName == "" {
		defaultServiceName = "mcp-beaver"
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String("service.name", defaultServiceName)),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, fmt.Errorf("configure OpenTelemetry resource: %w", err)
	}
	if serviceName, ok := os.LookupEnv("OTEL_SERVICE_NAME"); ok {
		serviceName = strings.TrimSpace(serviceName)
		if serviceName != "" {
			res, err = resource.Merge(res, resource.NewSchemaless(attribute.String("service.name", serviceName)))
			if err != nil {
				return nil, fmt.Errorf("configure OpenTelemetry service name: %w", err)
			}
		}
	}
	return res, nil
}
