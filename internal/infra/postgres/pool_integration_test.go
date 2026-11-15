//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/Zuneve/template/internal/config"
	"github.com/Zuneve/template/internal/infra/postgres"
)

func TestPool_TracesAndMeasuresQueries(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	pool, err := postgres.NewPool(t.Context(), config.DatabaseConfig{
		URL:            os.Getenv("DATABASE_URL"),
		MaxConns:       2,
		ConnectTimeout: 5 * time.Second,
	}, tp, mp)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// Queries are traced only inside a request span, as in the running service.
	ctx, parent := tp.Tracer("test").Start(t.Context(), "request")
	defer parent.End()

	var n int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM trips`).Scan(&n))
	_, err = pool.Exec(ctx, `SELECT * FROM no_such_table`)
	require.Error(t, err)

	attrsOf := func(span sdktrace.ReadOnlySpan) map[attribute.Key]string {
		out := map[attribute.Key]string{}
		for _, kv := range span.Attributes() {
			out[kv.Key] = kv.Value.String()
		}
		return out
	}

	var selectSpan sdktrace.ReadOnlySpan
	for _, s := range spans.Ended() {
		if s.Name() == "SELECT trips" {
			selectSpan = s
		}
	}
	require.NotNil(t, selectSpan, "span is named after the operation and the table")
	require.Equal(t, "trips", attrsOf(selectSpan)["db.collection.name"])

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	var points []metricdata.HistogramDataPoint[float64]
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == "db.client.operation.duration" {
				points = append(points, m.Data.(metricdata.Histogram[float64]).DataPoints...)
			}
		}
	}

	var ok, failed bool
	for _, p := range points {
		a := map[string]string{}
		for _, kv := range p.Attributes.ToSlice() {
			a[string(kv.Key)] = kv.Value.String()
		}
		switch a["db.collection.name"] {
		case "trips":
			ok = a["db.operation.name"] == "SELECT" && a["error.type"] == ""
		case "no_such_table":
			failed = a["error.type"] == "42P01" // undefined_table
		}
	}
	require.True(t, ok, "successful query is recorded with operation and collection: %v", points)
	require.True(t, failed, "failed query carries the SQLSTATE as error.type: %v", points)
}
