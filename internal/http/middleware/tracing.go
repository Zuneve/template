package middleware

import (
	"net/http"
	"strconv"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/Zuneve/template/internal/http/middleware"

// Tracing opens the root span of an incoming request. It continues the trace
// the caller started when the request carries a W3C traceparent header. The
// span is named "{method} {route}" so that all trips share one span name.
func Tracing(tp trace.TracerProvider, propagator propagation.TextMapPropagator, route RouteResolver) func(http.Handler) http.Handler {
	tracer := tp.Tracer(tracerName)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			routeName := route(r)
			spanName := r.Method
			attrs := []attribute.KeyValue{semconv.HTTPRequestMethodKey.String(r.Method), semconv.URLPath(r.URL.Path)}
			if routeName != "" {
				spanName += " " + routeName
				attrs = append(attrs, semconv.HTTPRoute(routeName))
			}

			ctx, span := tracer.Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attrs...),
			)
			defer span.End()

			rec := recordStatus(w)
			completed := false
			defer func() {
				status := rec.statusOf(completed)
				span.SetAttributes(semconv.HTTPResponseStatusCode(status))
				// A 4xx is the client's mistake, not a failure of this server.
				if status >= http.StatusInternalServerError {
					span.SetAttributes(semconv.ErrorTypeKey.String(strconv.Itoa(status)))
					span.SetStatus(codes.Error, http.StatusText(status))
				}
			}()

			next.ServeHTTP(rec, r.WithContext(ctx))
			completed = true
		})
	}
}
