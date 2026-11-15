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
	positionsSaved metric.Int64Counter

	staleChecks      metric.Int64Counter
	positionRequests metric.Int64Counter
	requestDuration  metric.Float64Histogram
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

	positionsSaved, err := meter.Int64Counter("tripgo.positions.saved",
		metric.WithUnit("{position}"),
		metric.WithDescription("Trip positions saved."),
	)
	if err != nil {
		return nil, fmt.Errorf("create tripgo.positions.saved: %w", err)
	}

	staleChecks, err := meter.Int64Counter("tripgo.stale_checks",
		metric.WithUnit("{trip}"),
		metric.WithDescription("What the scanner did with a stale trip on a tick: queued, duplicate or dropped."),
	)
	if err != nil {
		return nil, fmt.Errorf("create tripgo.stale_checks: %w", err)
	}

	positionRequests, err := meter.Int64Counter("tripgo.position_requests",
		metric.WithUnit("{request}"),
		metric.WithDescription("How an attempt to request a position ended: success, error or dropped."),
	)
	if err != nil {
		return nil, fmt.Errorf("create tripgo.position_requests: %w", err)
	}

	requestDuration, err := meter.Float64Histogram("tripgo.position_requests.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of the Push Service call that requests a position."),
		metric.WithExplicitBucketBoundaries(DurationBuckets...),
	)
	if err != nil {
		return nil, fmt.Errorf("create tripgo.position_requests.duration: %w", err)
	}

	return &Metrics{
		meter:          meter,
		requestTime:    requestTime,
		activeRequests: activeRequests,
		tripsCreated:   tripsCreated,
		tripsCompleted: tripsCompleted,
		positionsSaved: positionsSaved,

		staleChecks:      staleChecks,
		positionRequests: positionRequests,
		requestDuration:  requestDuration,
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

func (m *Metrics) PositionSaved(ctx context.Context) {
	m.positionsSaved.Add(ctx, 1)
}

// StaleCheck counts what the scanner did with one stale trip: result is
// "queued", "duplicate" or "dropped".
func (m *Metrics) StaleCheck(ctx context.Context, result string) {
	m.staleChecks.Add(ctx, 1, metric.WithAttributes(attribute.String("result", result)))
}

// PositionRequestDropped counts a task thrown away because the queue was full.
func (m *Metrics) PositionRequestDropped(ctx context.Context) {
	m.positionRequests.Add(ctx, 1, metric.WithAttributes(attribute.String("result", "dropped")))
}

// PositionRequestFinished records the end of a Push Service call: result is
// "success" or "error". Both the counter and the duration histogram get it.
func (m *Metrics) PositionRequestFinished(ctx context.Context, result string, duration time.Duration) {
	attrs := metric.WithAttributes(attribute.String("result", result))
	m.positionRequests.Add(ctx, 1, attrs)
	m.requestDuration.Record(ctx, duration.Seconds(), attrs)
}

// ObservePositionRequests registers the queue and worker gauges. Both are read
// at collection time from the component that owns the number.
func (m *Metrics) ObservePositionRequests(queueSize, workersActive func() int64) error {
	if _, err := m.meter.Int64ObservableGauge("tripgo.position_requests.queue.size",
		metric.WithUnit("{task}"),
		metric.WithDescription("Tasks currently waiting in the position request queue."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(queueSize())
			return nil
		}),
	); err != nil {
		return fmt.Errorf("create tripgo.position_requests.queue.size: %w", err)
	}

	if _, err := m.meter.Int64ObservableGauge("tripgo.position_requests.workers.active",
		metric.WithUnit("{worker}"),
		metric.WithDescription("Workers currently busy with a position request."),
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(workersActive())
			return nil
		}),
	); err != nil {
		return fmt.Errorf("create tripgo.position_requests.workers.active: %w", err)
	}
	return nil
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
