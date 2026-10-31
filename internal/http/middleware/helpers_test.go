package middleware_test

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

func telemetrySpanID(ctx context.Context) string {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ""
	}
	return sc.SpanID().String()
}
