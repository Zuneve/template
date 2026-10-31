package postgres

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/Zuneve/template/internal/infra/telemetry"
)

const (
	meterName      = "github.com/Zuneve/template/internal/infra/postgres"
	unknownErrType = "_OTHER"
)

var collectionPattern = regexp.MustCompile(`(?i)\b(?:from|into|update)\s+([a-z_][a-z0-9_.]*)`)

type queryState struct {
	start      time.Time
	operation  string
	collection string
}

type stateKey struct{}

// queryTracer instruments every query of the pool. Spans come from otelpgx;
// it does not know the table a query touches and its duration metric lacks
// db.collection.name, so this wrapper adds that attribute to the span and
// records db.client.operation.duration itself with the labels the service
// catalog fixes: db.operation.name, db.collection.name, error.type.
type queryTracer struct {
	spans    *otelpgx.Tracer
	duration metric.Float64Histogram
}

func newQueryTracer(tp trace.TracerProvider, mp metric.MeterProvider) (*queryTracer, error) {
	duration, err := mp.Meter(meterName).Float64Histogram("db.client.operation.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of database client operations."),
		metric.WithExplicitBucketBoundaries(telemetry.DurationBuckets...),
	)
	if err != nil {
		return nil, err
	}

	return &queryTracer{
		spans: otelpgx.NewTracer(
			otelpgx.WithTracerProvider(tp),
			otelpgx.WithMeterProvider(noop.NewMeterProvider()),
			otelpgx.WithDisableAcquireTracer(),
		),
		duration: duration,
	}, nil
}

func (t *queryTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	state := queryState{
		start:      time.Now(),
		operation:  operationName(ctx, data.SQL),
		collection: collectionName(data.SQL),
	}

	ctx = t.spans.TraceQueryStart(ctx, conn, data)
	if span := trace.SpanFromContext(ctx); span.IsRecording() && state.collection != "" {
		span.SetAttributes(semconv.DBCollectionName(state.collection))
		if state.operation != "" {
			span.SetName(state.operation + " " + state.collection)
		}
	}

	return context.WithValue(ctx, stateKey{}, state)
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	t.spans.TraceQueryEnd(ctx, conn, data)

	state, ok := ctx.Value(stateKey{}).(queryState)
	if !ok {
		return
	}

	attrs := []attribute.KeyValue{semconv.DBSystemNamePostgreSQL}
	if state.operation != "" {
		attrs = append(attrs, semconv.DBOperationName(state.operation))
	}
	if state.collection != "" {
		attrs = append(attrs, semconv.DBCollectionName(state.collection))
	}
	if data.Err != nil {
		attrs = append(attrs, semconv.ErrorTypeKey.String(errorType(data.Err)))
	}

	t.duration.Record(ctx, time.Since(state.start).Seconds(), metric.WithAttributes(attrs...))
}

func operationName(ctx context.Context, sql string) string {
	return strings.ToUpper(otelpgx.SQLOperationName(ctx, sql))
}

func collectionName(sql string) string {
	match := collectionPattern.FindStringSubmatch(sql)
	if match == nil {
		return ""
	}
	return strings.ToLower(match[1])
}

// errorType is a low-cardinality error.type: the SQLSTATE for errors the
// server reported, otherwise a coarse class.
func errorType(err error) string {
	var pgErr *pgconn.PgError
	switch {
	case errors.As(err, &pgErr):
		return pgErr.Code
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	default:
		return unknownErrType
	}
}
