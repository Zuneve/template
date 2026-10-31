package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/middleware"
	"github.com/Zuneve/template/internal/infra/telemetry"
)

const (
	routeTemplate = "/api/v1/trips/{tripId}"
	levelInfo     = "INFO"
	levelError    = "ERROR"
)

func staticRoute(name string) middleware.RouteResolver {
	return func(*http.Request) string { return name }
}

func handlerWithStatus(status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
	})
}

var panicking = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
	panic("kaboom")
})

func newRequest() *http.Request {
	return httptest.NewRequest(http.MethodGet, "/api/v1/trips/abc", nil)
}

func logRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal(line, &rec))
		records = append(records, rec)
	}
	return records
}

func TestRecover(t *testing.T) {
	t.Run("panic becomes a logged 500 without details", func(t *testing.T) {
		var buf bytes.Buffer
		h := middleware.Recover(telemetry.NewLogger(&buf, slog.LevelInfo))(panicking)
		rec := httptest.NewRecorder()

		require.NotPanics(t, func() { h.ServeHTTP(rec, newRequest()) })

		require.Equal(t, http.StatusInternalServerError, rec.Code)
		require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
		var p api.Problem
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
		require.Equal(t, "internal_error", p.Code)
		require.NotContains(t, rec.Body.String(), "kaboom")

		records := logRecords(t, &buf)
		require.Len(t, records, 1)
		require.Equal(t, levelError, records[0]["level"])
		require.Equal(t, "panic recovered", records[0]["msg"])
		require.Equal(t, "kaboom", records[0]["panic"])
		require.Contains(t, records[0]["stack"], "middleware_test.go", "the stack points at the panicking code")
	})

	t.Run("normal request passes through", func(t *testing.T) {
		h := middleware.Recover(slog.New(slog.DiscardHandler))(handlerWithStatus(http.StatusNoContent))
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, newRequest())

		require.Equal(t, http.StatusNoContent, rec.Code)
	})

	t.Run("ErrAbortHandler is passed on to net/http", func(t *testing.T) {
		abort := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
		h := middleware.Recover(slog.New(slog.DiscardHandler))(abort)

		require.PanicsWithError(t, http.ErrAbortHandler.Error(), func() {
			h.ServeHTTP(httptest.NewRecorder(), newRequest())
		})
	})
}

func TestTimeout_SetsDeadline(t *testing.T) {
	var deadline time.Time
	var ok bool
	h := middleware.Timeout(time.Minute)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		deadline, ok = r.Context().Deadline()
	}))

	h.ServeHTTP(httptest.NewRecorder(), newRequest())

	require.True(t, ok)
	require.WithinDuration(t, time.Now().Add(time.Minute), deadline, 5*time.Second)
}

func TestLogging(t *testing.T) {
	tests := []struct {
		name      string
		next      http.Handler
		wantLevel string
		wantCode  float64
	}{
		{"200 is info", handlerWithStatus(http.StatusOK), levelInfo, 200},
		{"404 is info", handlerWithStatus(http.StatusNotFound), levelInfo, 404},
		{"500 is error", handlerWithStatus(http.StatusInternalServerError), levelError, 500},
		{"implicit 200", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }), levelInfo, 200},
		{"no write at all is 200", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), levelInfo, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := middleware.Logging(telemetry.NewLogger(&buf, slog.LevelInfo), staticRoute(routeTemplate))(tt.next)

			h.ServeHTTP(httptest.NewRecorder(), newRequest())

			records := logRecords(t, &buf)
			require.Len(t, records, 1)
			rec := records[0]
			require.Equal(t, "request handled", rec["msg"])
			require.Equal(t, tt.wantLevel, rec["level"])
			require.Equal(t, tt.wantCode, rec["status"])
			require.Equal(t, "GET", rec["method"])
			require.Equal(t, routeTemplate, rec["route"])
			require.Contains(t, rec, "duration_ms")
			require.NotContains(t, rec, "path", "the concrete path stays out of the log")
		})
	}
}

func TestLogging_PanicIsLoggedAs500AndPropagates(t *testing.T) {
	var buf bytes.Buffer
	h := middleware.Logging(telemetry.NewLogger(&buf, slog.LevelInfo), staticRoute(routeTemplate))(panicking)

	require.PanicsWithValue(t, "kaboom", func() { h.ServeHTTP(httptest.NewRecorder(), newRequest()) })

	records := logRecords(t, &buf)
	require.Len(t, records, 1)
	require.Equal(t, levelError, records[0]["level"])
	require.Equal(t, float64(500), records[0]["status"])
}

func TestLogging_AddsTraceIDsInsideSpan(t *testing.T) {
	var buf bytes.Buffer
	tp := sdktrace.NewTracerProvider()
	logging := middleware.Logging(telemetry.NewLogger(&buf, slog.LevelInfo), staticRoute(routeTemplate))
	h := middleware.Tracing(tp, propagation.TraceContext{}, staticRoute(routeTemplate))(logging(handlerWithStatus(http.StatusOK)))

	h.ServeHTTP(httptest.NewRecorder(), newRequest())

	records := logRecords(t, &buf)
	require.Len(t, records, 1)
	require.Len(t, records[0]["trace_id"], 32)
	require.Len(t, records[0]["span_id"], 16)
}

type fakeMetrics struct {
	started  []string
	finished []finishedCall
}

type finishedCall struct {
	method, route string
	status        int
	duration      time.Duration
}

func (f *fakeMetrics) RequestStarted(_ context.Context, method, route string) {
	f.started = append(f.started, method+" "+route)
}

func (f *fakeMetrics) RequestFinished(_ context.Context, method, route string, status int, d time.Duration) {
	f.finished = append(f.finished, finishedCall{method, route, status, d})
}

func TestMetrics(t *testing.T) {
	tests := []struct {
		name       string
		next       http.Handler
		wantStatus int
		panics     bool
	}{
		{"success", handlerWithStatus(http.StatusCreated), 201, false},
		{"client error", handlerWithStatus(http.StatusConflict), 409, false},
		{"panic counts as 500", panicking, 500, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &fakeMetrics{}
			h := middleware.Metrics(m, staticRoute(routeTemplate))(tt.next)

			call := func() { h.ServeHTTP(httptest.NewRecorder(), newRequest()) }
			if tt.panics {
				require.Panics(t, call)
			} else {
				call()
			}

			require.Equal(t, []string{"GET " + routeTemplate}, m.started)
			require.Len(t, m.finished, 1, "every started request is finished, otherwise active_requests leaks")
			got := m.finished[0]
			require.Equal(t, "GET", got.method)
			require.Equal(t, routeTemplate, got.route)
			require.Equal(t, tt.wantStatus, got.status)
			require.GreaterOrEqual(t, got.duration, time.Duration(0))
		})
	}
}

func spanAttrs(span sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	attrs := map[attribute.Key]attribute.Value{}
	for _, kv := range span.Attributes() {
		attrs[kv.Key] = kv.Value
	}
	return attrs
}

func TestTracing(t *testing.T) {
	tests := []struct {
		name       string
		route      string
		next       http.Handler
		wantName   string
		wantStatus int
		wantError  bool
		panics     bool
	}{
		{"ok", routeTemplate, handlerWithStatus(http.StatusOK), "GET " + routeTemplate, 200, false, false},
		{"4xx is not an error", routeTemplate, handlerWithStatus(http.StatusNotFound), "GET " + routeTemplate, 404, false, false},
		{"5xx is an error", routeTemplate, handlerWithStatus(http.StatusBadGateway), "GET " + routeTemplate, 502, true, false},
		{"unknown route has no template", "", handlerWithStatus(http.StatusNotFound), "GET", 404, false, false},
		{"panic is a 500 error", routeTemplate, panicking, "GET " + routeTemplate, 500, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := tracetest.NewSpanRecorder()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			h := middleware.Tracing(tp, propagation.TraceContext{}, staticRoute(tt.route))(tt.next)

			call := func() { h.ServeHTTP(httptest.NewRecorder(), newRequest()) }
			if tt.panics {
				require.Panics(t, call)
			} else {
				call()
			}

			spans := recorder.Ended()
			require.Len(t, spans, 1)
			span := spans[0]
			require.Equal(t, tt.wantName, span.Name())
			require.Equal(t, trace.SpanKindServer, span.SpanKind())

			attrs := spanAttrs(span)
			require.Equal(t, int64(tt.wantStatus), attrs["http.response.status_code"].AsInt64())
			require.Equal(t, "GET", attrs["http.request.method"].AsString())
			require.Equal(t, "/api/v1/trips/abc", attrs["url.path"].AsString())
			if tt.route == "" {
				require.NotContains(t, attrs, attribute.Key("http.route"))
			} else {
				require.Equal(t, tt.route, attrs["http.route"].AsString())
			}

			if tt.wantError {
				require.Equal(t, codes.Error, span.Status().Code)
				require.Contains(t, attrs, attribute.Key("error.type"))
			} else {
				require.Equal(t, codes.Unset, span.Status().Code)
				require.NotContains(t, attrs, attribute.Key("error.type"))
			}
		})
	}
}

func TestTracing_ContinuesIncomingTrace(t *testing.T) {
	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	h := middleware.Tracing(tp, propagation.TraceContext{}, staticRoute(routeTemplate))(handlerWithStatus(http.StatusOK))
	req := newRequest()
	req.Header.Set("traceparent", "00-"+traceID+"-00f067aa0ba902b7-01")

	h.ServeHTTP(httptest.NewRecorder(), req)

	spans := recorder.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, traceID, spans[0].SpanContext().TraceID().String())
	require.Equal(t, "00f067aa0ba902b7", spans[0].Parent().SpanID().String())
}

func TestTracing_HandlerSeesTheSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	var inHandler string
	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		inHandler = telemetrySpanID(r.Context())
	})

	middleware.Tracing(tp, propagation.TraceContext{}, staticRoute(routeTemplate))(next).ServeHTTP(httptest.NewRecorder(), newRequest())

	require.NotEmpty(t, inHandler)
	require.Equal(t, recorder.Ended()[0].SpanContext().SpanID().String(), inHandler)
}

func TestObservabilityMiddlewares_KeepResponseControllerWorking(t *testing.T) {
	var flushErr error
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flushErr = http.NewResponseController(w).Flush()
	})
	rec := httptest.NewRecorder()
	h := middleware.Logging(slog.New(slog.DiscardHandler), staticRoute(routeTemplate))(next)

	h.ServeHTTP(rec, newRequest())

	require.NoError(t, flushErr)
	require.True(t, rec.Flushed)
}

func TestStatusRecorder_IsSharedAcrossMiddlewares(t *testing.T) {
	m := &fakeMetrics{}
	var buf bytes.Buffer
	chain := middleware.Logging(telemetry.NewLogger(&buf, slog.LevelInfo), staticRoute(routeTemplate))(
		middleware.Metrics(m, staticRoute(routeTemplate))(handlerWithStatus(http.StatusTeapot)),
	)

	chain.ServeHTTP(httptest.NewRecorder(), newRequest())

	require.Equal(t, http.StatusTeapot, m.finished[0].status)
	require.Equal(t, float64(http.StatusTeapot), logRecords(t, &buf)[0]["status"])
}
