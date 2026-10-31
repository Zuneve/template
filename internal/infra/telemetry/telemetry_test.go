package telemetry_test

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Zuneve/template/internal/infra/telemetry"
)

func TestSetup_RejectsUnsupportedProtocol(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")

	p, err := telemetry.Setup(t.Context(), "test", slog.New(slog.DiscardHandler))

	require.ErrorContains(t, err, "grpc")
	require.Nil(t, p)
}

func TestSetup_SucceedsWithoutCollectorAndShutsDownQuickly(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_SERVICE_NAME", "svc-under-test")

	p, err := telemetry.Setup(t.Context(), "test", slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	require.NotNil(t, p.TracerProvider())
	require.NotNil(t, p.MeterProvider())
	require.NotEmpty(t, p.Propagator().Fields(), "the propagator carries traceparent")

	_, span := p.TracerProvider().Tracer("test").Start(t.Context(), "op")
	span.End()

	// The collector is down: shutdown may report that, but it must return.
	_ = p.Shutdown(t.Context())
}
