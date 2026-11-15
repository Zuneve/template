package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/Zuneve/template/internal/infra/telemetry"
)

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	return rec
}

func TestNewLogger_WritesJSONWithService(t *testing.T) {
	var buf bytes.Buffer

	telemetry.NewLogger(&buf, slog.LevelInfo).Info("hello", slog.Int("n", 1))

	rec := decode(t, &buf)
	require.Equal(t, "hello", rec["msg"])
	require.Equal(t, "INFO", rec["level"])
	require.Equal(t, "trip-service", rec["service"])
	require.InDelta(t, 1, rec["n"], 0)
	require.NotContains(t, rec, "trace_id", "no span, no trace ids")
	require.NotContains(t, rec, "span_id")
}

func TestNewLogger_AddsTraceIDsFromContext(t *testing.T) {
	var buf bytes.Buffer
	ctx, span := sdktrace.NewTracerProvider().Tracer("test").Start(context.Background(), "op")
	defer span.End()

	telemetry.NewLogger(&buf, slog.LevelInfo).InfoContext(ctx, "inside")

	rec := decode(t, &buf)
	require.Equal(t, span.SpanContext().TraceID().String(), rec["trace_id"])
	require.Equal(t, span.SpanContext().SpanID().String(), rec["span_id"])
}

func TestNewLogger_KeepsTraceIDsAfterWithAttrsAndGroup(t *testing.T) {
	var buf bytes.Buffer
	ctx, span := sdktrace.NewTracerProvider().Tracer("test").Start(context.Background(), "op")
	defer span.End()

	telemetry.NewLogger(&buf, slog.LevelInfo).With("k", "v").WithGroup("g").InfoContext(ctx, "grouped", "x", 1)

	rec := decode(t, &buf)
	require.Equal(t, "v", rec["k"])
	require.Equal(t, map[string]any{"x": float64(1), "trace_id": span.SpanContext().TraceID().String(), "span_id": span.SpanContext().SpanID().String()}, rec["g"])
}

func TestNewLogger_RespectsLevel(t *testing.T) {
	var buf bytes.Buffer

	logger := telemetry.NewLogger(&buf, slog.LevelWarn)
	logger.Info("quiet")
	require.Empty(t, buf.String())

	logger.Warn("loud")
	require.Contains(t, buf.String(), "loud")
}
