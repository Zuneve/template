package telemetry_test

import (
	"context"

	"go.opentelemetry.io/otel/trace"
)

func traceSampled(ctx context.Context) bool {
	return trace.SpanContextFromContext(ctx).IsSampled()
}
