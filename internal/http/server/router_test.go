package server_test

import (
	"context"
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

	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/server"
)

var tripID = uuid.MustParse("66666666-6666-6666-6666-666666666666")

// tag is a fake handler that answers 204 and says which operation was routed.
type tag string

func (h tag) mark(w http.ResponseWriter) {
	w.Header().Set("X-Handler", string(h))
	w.WriteHeader(http.StatusNoContent)
}

type createFake struct {
	tag
	gotKey *uuid.UUID
}

func (h *createFake) Handle(w http.ResponseWriter, _ *http.Request, params api.CreateTripParams) {
	h.gotKey = params.IdempotencyKey
	h.mark(w)
}

type tripFake struct {
	tag
	gotID uuid.UUID
}

func (h *tripFake) Handle(w http.ResponseWriter, _ *http.Request, id api.TripId) {
	h.gotID = id
	h.mark(w)
}

type healthFake struct{ tag }

func (h healthFake) Live(w http.ResponseWriter, _ *http.Request)  { h.mark(w) }
func (h healthFake) Ready(w http.ResponseWriter, _ *http.Request) { h.tag = "ready"; h.mark(w) }

type metricsFake struct{ routes []string }

func (m *metricsFake) RequestStarted(_ context.Context, method, route string) {
	m.routes = append(m.routes, method+" "+route)
}
func (m *metricsFake) RequestFinished(context.Context, string, string, int, time.Duration) {}

type env struct {
	handler http.Handler
	create  *createFake
	get     *tripFake
	finish  *tripFake
	metrics *metricsFake
	spans   *tracetest.SpanRecorder
}

func newEnv() *env {
	e := &env{
		create:  &createFake{tag: "create"},
		get:     &tripFake{tag: "get"},
		finish:  &tripFake{tag: "finish"},
		metrics: &metricsFake{},
		spans:   tracetest.NewSpanRecorder(),
	}
	e.handler = server.NewRouter(e.create, e.get, e.finish, healthFake{"live"}, server.Deps{
		Logger:         slog.New(slog.DiscardHandler),
		RequestTimeout: time.Second,
		TracerProvider: sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(e.spans)),
		Propagator:     propagation.TraceContext{},
		Metrics:        e.metrics,
	})
	return e
}

func (e *env) do(method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestRouter_RoutesToHandlers(t *testing.T) {
	tests := []struct {
		method, path string
		wantHandler  string
	}{
		{http.MethodPost, "/api/v1/trips", "create"},
		{http.MethodGet, "/api/v1/trips/" + tripID.String(), "get"},
		{http.MethodPost, "/api/v1/trips/" + tripID.String() + "/finish", "finish"},
		{http.MethodGet, "/health", "live"},
		{http.MethodGet, "/ready", "ready"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := newEnv().do(tt.method, tt.path)

			require.Equal(t, http.StatusNoContent, rec.Code)
			require.Equal(t, tt.wantHandler, rec.Header().Get("X-Handler"))
		})
	}
}

func TestRouter_PassesParameters(t *testing.T) {
	e := newEnv()
	key := uuid.New()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/trips", nil)
	req.Header.Set("Idempotency-Key", key.String())
	e.handler.ServeHTTP(httptest.NewRecorder(), req)
	e.do(http.MethodGet, "/api/v1/trips/"+tripID.String())
	e.do(http.MethodPost, "/api/v1/trips/"+tripID.String()+"/finish")

	require.NotNil(t, e.create.gotKey)
	require.Equal(t, key, *e.create.gotKey)
	require.Equal(t, tripID, e.get.gotID)
	require.Equal(t, tripID, e.finish.gotID)
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
			e := newEnv()
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
			require.Empty(t, rec.Header().Get("X-Handler"), "the handler is not reached")
		})
	}
}

func TestRouter_UnimplementedOperation_Is501(t *testing.T) {
	rec := newEnv().do(http.MethodPost, "/api/v1/trips/"+tripID.String()+"/positions")

	require.Equal(t, http.StatusNotImplemented, rec.Code)
}

func TestRouter_ObservabilityUsesRouteTemplate(t *testing.T) {
	e := newEnv()

	e.do(http.MethodGet, "/api/v1/trips/"+tripID.String())

	require.Equal(t, []string{"GET /api/v1/trips/{tripId}"}, e.metrics.routes)
	spans := e.spans.Ended()
	require.Len(t, spans, 1)
	require.Equal(t, "GET /api/v1/trips/{tripId}", spans[0].Name())
}

func TestRouter_UnknownPath_IsObservedWithoutRoute(t *testing.T) {
	e := newEnv()

	rec := e.do(http.MethodGet, "/nope")

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, []string{"GET "}, e.metrics.routes)
	require.Equal(t, "GET", e.spans.Ended()[0].Name())
}
