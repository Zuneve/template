package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	meterName = "github.com/Zuneve/template"

	activeTripsTimeout = 3 * time.Second
)

// DurationBuckets are histogram boundaries in seconds. The SDK defaults are
// meant for milliseconds and would put every request into the first bucket.
var DurationBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Metrics holds the service-level instruments from docs/observability.md. The
// db.client.operation.duration histogram lives next to the pgx tracer that
// records it.
type Metrics struct {
	meter          metric.Meter
	requestTime    metric.Float64Histogram
	activeRequests metric.Int64UpDownCounter
	tripsCreated   metric.Int64Counter
	tripsCompleted metric.Int64Counter
}

func NewMetrics(provider metric.MeterProvider) (*Metrics, error) {
	meter := provider.Meter(meterName)

	requestTime, err := meter.Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of HTTP server requests."),
		metric.WithExplicitBucketBoundaries(DurationBuckets...),
	)
	if err != nil {
		return nil, fmt.Errorf("create http.server.request.duration: %w", err)
	}

	activeRequests, err := meter.Int64UpDownCounter("http.server.active_requests",
		metric.WithUnit("{request}"),
		metric.WithDescription("Number of HTTP requests being processed."),
	)
	if err != nil {
		return nil, fmt.Errorf("create http.server.active_requests: %w", err)
	}

	tripsCreated, err := meter.Int64Counter("tripgo.trips.created",
		metric.WithUnit("{trip}"),
		metric.WithDescription("Trips created."),
	)
	if err != nil {
		return nil, fmt.Errorf("create tripgo.trips.created: %w", err)
	}

	tripsCompleted, err := meter.Int64Counter("tripgo.trips.completed",
		metric.WithUnit("{trip}"),
		metric.WithDescription("Trips completed."),
	)
	if err != nil {
		return nil, fmt.Errorf("create tripgo.trips.completed: %w", err)
	}

	return &Metrics{
		meter:          meter,
		requestTime:    requestTime,
		activeRequests: activeRequests,
		tripsCreated:   tripsCreated,
		tripsCompleted: tripsCompleted,
	}, nil
}

// RequestStarted counts a request as in flight. route is the router's
// template ("/api/v1/trips/{tripId}"), never the concrete path: a path with a
// trip id would create a separate time series per trip.
func (m *Metrics) RequestStarted(ctx context.Context, method, route string) {
	m.activeRequests.Add(ctx, 1, metric.WithAttributes(requestAttrs(method, route)...))
}

func (m *Metrics) RequestFinished(ctx context.Context, method, route string, status int, duration time.Duration) {
	m.activeRequests.Add(ctx, -1, metric.WithAttributes(requestAttrs(method, route)...))

	attrs := append(requestAttrs(method, route), semconv.HTTPResponseStatusCode(status))
	m.requestTime.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs...))
}

func (m *Metrics) TripCreated(ctx context.Context) {
	m.tripsCreated.Add(ctx, 1)
}

func (m *Metrics) TripCompleted(ctx context.Context) {
	m.tripsCompleted.Add(ctx, 1)
}

// ObserveActiveTrips registers the tripgo.trips.active gauge. The value is
// read from the source of truth at collection time instead of being
// maintained by increments, so it stays right after a restart.
func (m *Metrics) ObserveActiveTrips(count func(ctx context.Context) (int64, error)) error {
	_, err := m.meter.Int64ObservableGauge("tripgo.trips.active",
		metric.WithUnit("{trip}"),
		metric.WithDescription("Trips currently in the active status."),
		metric.WithInt64Callback(func(ctx context.Context, o metric.Int64Observer) error {
			ctx, cancel := context.WithTimeout(withoutSampling(ctx), activeTripsTimeout)
			defer cancel()

			n, err := count(ctx)
			if err != nil {
				return fmt.Errorf("count active trips: %w", err)
			}
			o.Observe(n)
			return nil
		}),
	)
	if err != nil {
		return fmt.Errorf("create tripgo.trips.active: %w", err)
	}
	return nil
}

func requestAttrs(method, route string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 3)
	attrs = append(attrs, semconv.HTTPRequestMethodKey.String(method))
	if route != "" {
		attrs = append(attrs, semconv.HTTPRoute(route))
	}
	return attrs
}

// withoutSampling marks ctx as belonging to an unsampled trace, so database
// calls made by background collection do not litter Jaeger with one-span
// traces every export interval.
func withoutSampling(ctx context.Context) context.Context {
	return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{1},
		TraceFlags: 0,
		Remote:     true,
	}))
}
