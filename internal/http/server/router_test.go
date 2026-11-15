package server_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"

	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/server"
)

var tripID = uuid.MustParse("66666666-6666-6666-6666-666666666666")

type env struct {
	handler http.Handler
	create  *MockcreateTripHandler
	get     *MockgetTripHandler
	finish  *MockfinishTripHandler
	health  *MockhealthHandler
	metrics *MockhttpMetrics
	spans   *tracetest.SpanRecorder
}

// newEnv builds the router over strict mocks: a call nobody expected fails
// the test, so "the handler is not reached" needs no extra assertion.
func newEnv(t *testing.T) *env {
	t.Helper()

	ctrl := gomock.NewController(t)
	e := &env{
		create:  NewMockcreateTripHandler(ctrl),
		get:     NewMockgetTripHandler(ctrl),
		finish:  NewMockfinishTripHandler(ctrl),
		health:  NewMockhealthHandler(ctrl),
		metrics: NewMockhttpMetrics(ctrl),
		spans:   tracetest.NewSpanRecorder(),
	}
	e.handler = server.NewRouter(e.create, e.get, e.finish, e.health, server.Deps{
		Logger:         slog.New(slog.DiscardHandler),
		RequestTimeout: time.Second,
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(e.spans)),
		Propagator:     propagation.TraceContext{},
		Metrics:        e.metrics,
	})
	return e
}

// ignoreMetrics is for tests that are not about metrics.
func (e *env) ignoreMetrics() {
	e.metrics.EXPECT().RequestStarted(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
	e.metrics.EXPECT().RequestFinished(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
}

func (e *env) do(method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// answered is what a routed handler does in these tests: it says who it is.
func answered(w http.ResponseWriter, handler string) {
	w.Header().Set("X-Handler", handler)
	w.WriteHeader(http.StatusNoContent)
}

func TestRouter_RoutesToHandlers(t *testing.T) {
	tests := []struct {
		method, path string
		wantHandler  string
		expect       func(e *env, answer func(w http.ResponseWriter))
	}{
		{http.MethodPost, "/api/v1/trips", "create", func(e *env, answer func(http.ResponseWriter)) {
			e.create.EXPECT().Handle(gomock.Any(), gomock.Any(), gomock.Any()).
				Do(func(w http.ResponseWriter, _ *http.Request, _ api.CreateTripParams) { answer(w) })
		}},
		{http.MethodGet, "/api/v1/trips/" + tripID.String(), "get", func(e *env, answer func(http.ResponseWriter)) {
			e.get.EXPECT().Handle(gomock.Any(), gomock.Any(), tripID).
				Do(func(w http.ResponseWriter, _ *http.Request, _ api.TripId) { answer(w) })
		}},
		{http.MethodPost, "/api/v1/trips/" + tripID.String() + "/finish", "finish", func(e *env, answer func(http.ResponseWriter)) {
			e.finish.EXPECT().Handle(gomock.Any(), gomock.Any(), tripID).
				Do(func(w http.ResponseWriter, _ *http.Request, _ api.TripId) { answer(w) })
		}},
		{http.MethodGet, "/health", "live", func(e *env, answer func(http.ResponseWriter)) {
			e.health.EXPECT().Live(gomock.Any(), gomock.Any()).
				Do(func(w http.ResponseWriter, _ *http.Request) { answer(w) })
		}},
		{http.MethodGet, "/ready", "ready", func(e *env, answer func(http.ResponseWriter)) {
			e.health.EXPECT().Ready(gomock.Any(), gomock.Any()).
				Do(func(w http.ResponseWriter, _ *http.Request) { answer(w) })
		}},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			e := newEnv(t)
			e.ignoreMetrics()
			tt.expect(e, func(w http.ResponseWriter) { answered(w, tt.wantHandler) })

			rec := e.do(tt.method, tt.path)

			require.Equal(t, http.StatusNoContent, rec.Code)
			require.Equal(t, tt.wantHandler, rec.Header().Get("X-Handler"))
		})
	}
}

func TestRouter_PassesParameters(t *testing.T) {
	e := newEnv(t)
	e.ignoreMetrics()
	key := uuid.New()
	noContent := func(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

	e.create.EXPECT().Handle(gomock.Any(), gomock.Any(), api.CreateTripParams{IdempotencyKey: &key}).
		Do(func(w http.ResponseWriter, _ *http.Request, _ api.CreateTripParams) { noContent(w) })
	e.get.EXPECT().Handle(gomock.Any(), gomock.Any(), tripID).
		Do(func(w http.ResponseWriter, _ *http.Request, _ api.TripId) { noContent(w) })
	e.finish.EXPECT().Handle(gomock.Any(), gomock.Any(), tripID).
		Do(func(w http.ResponseWriter, _ *http.Request, _ api.TripId) { noContent(w) })

	req := httptest.NewRequest(http.MethodPost, "/api/v1/trips", nil)
	req.Header.Set("Idempotency-Key", key.String())
	e.handler.ServeHTTP(httptest.NewRecorder(), req)
	e.do(http.MethodGet, "/api/v1/trips/"+tripID.String())
	e.do(http.MethodPost, "/api/v1/trips/"+tripID.String()+"/finish")
}

func TestRouter_BadParameters_AreProblemJSON(t *testing.T) {
	tests := []struct {
		name         string
		method, path string
		header       string
	}{
		{"trip id is not a uuid", http.MethodGet, "/api/v1/trips/not-a-uuid", ""},
		{"finish with bad trip id", http.MethodPost, "/api/v1/trips/123/finish", ""},
		{"idempotency key is not a uuid", http.MethodPost, "/api/v1/trips", "nope"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.ignoreMetrics()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.header != "" {
				req.Header.Set("Idempotency-Key", tt.header)
			}
			rec := httptest.NewRecorder()

			e.handler.ServeHTTP(rec, req)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
			var p api.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			require.Equal(t, "invalid_request", p.Code)
		})
	}
}

func TestRouter_UnimplementedOperation_Is501(t *testing.T) {
	e := newEnv(t)
	e.ignoreMetrics()

	rec := e.do(http.MethodPost, "/api/v1/trips/"+tripID.String()+"/positions")

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestRouter_ObservabilityUsesRouteTemplate(t *testing.T) {
	e := newEnv(t)
	e.get.EXPECT().Handle(gomock.Any(), gomock.Any(), tripID).
		Do(func(w http.ResponseWriter, _ *http.Request, _ api.TripId) { w.WriteHeader(http.StatusNoContent) })
	e.metrics.EXPECT().RequestStarted(gomock.Any(), http.MethodGet, "/api/v1/trips/{tripId}")
	e.metrics.EXPECT().RequestFinished(gomock.Any(), http.MethodGet, "/api/v1/trips/{tripId}", http.StatusNoContent, gomock.Any())

	e.do(http.MethodGet, "/api/v1/trips/"+tripID.String())

	spans := e.spans.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, "GET /api/v1/trips/{tripId}", spans[0].Name())
}

func TestRouter_UnknownPath_IsObservedWithoutRoute(t *testing.T) {
	e := newEnv(t)
	e.metrics.EXPECT().RequestStarted(gomock.Any(), http.MethodGet, "")
	e.metrics.EXPECT().RequestFinished(gomock.Any(), http.MethodGet, "", http.StatusNotFound, gomock.Any())

	rec := e.do(http.MethodGet, "/nope")

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "GET", e.spans.Ended()[0].Name())
}
