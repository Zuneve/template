// Package telemetry sets up OpenTelemetry (traces and metrics over OTLP) and
// the JSON logger. Telemetry is not a critical dependency: an unreachable
// collector is reported in the log and never stops the service.
package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	// ServiceName is the default service.name; OTEL_SERVICE_NAME overrides it.
	ServiceName = "trip-service"

	protocolHTTPProtobuf = "http/protobuf"
)

// Provider owns the tracer and meter providers. They are handed to
// consumers explicitly instead of being registered as OpenTelemetry globals.
type Provider struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
}

// Setup builds the providers from the standard OTEL_* environment variables:
// OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_RESOURCE_ATTRIBUTES, OTEL_SERVICE_NAME,
// OTEL_TRACES_SAMPLER and OTEL_METRIC_EXPORT_INTERVAL. Exporters connect
// lazily, so Setup succeeds while the collector is down.
func Setup(ctx context.Context, version string, logger *slog.Logger) (*Provider, error) {
	if protocol := os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"); protocol != "" && protocol != protocolHTTPProtobuf {
		return nil, fmt.Errorf("OTEL_EXPORTER_OTLP_PROTOCOL %q is not supported, use %q", protocol, protocolHTTPProtobuf)
	}

	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		logger.Warn("telemetry export failed", slog.Any("error", err))
	}))

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(ServiceName),
			semconv.ServiceVersion(version),
			semconv.ServiceNamespace("tripgo"),
			semconv.DeploymentEnvironmentName("local"),
		),
		resource.WithFromEnv(),
	)
	if err != nil {
		return nil, fmt.Errorf("build resource: %w", err)
	}

	traceExporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create trace exporter: %w", err)
	}
	metricExporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("create metric exporter: %w", err)
	}

	return &Provider{
		tracerProvider: sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
			sdktrace.WithBatcher(traceExporter),
		),
		meterProvider: sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
		),
	}, nil
}

func (p *Provider) TracerProvider() trace.TracerProvider {
	return p.tracerProvider
}

func (p *Provider) MeterProvider() metric.MeterProvider {
	return p.meterProvider
}

// Propagator is the W3C Trace Context propagator the service uses to join
// traces of incoming requests.
func (p *Provider) Propagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}

// Shutdown flushes buffered telemetry. It is bounded by ctx, so a dead
// collector delays the exit by at most that long.
func (p *Provider) Shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return errors.Join(
		p.tracerProvider.Shutdown(ctx),
		p.meterProvider.Shutdown(ctx),
	)
}
