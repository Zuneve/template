package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/health"
)

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func status(t *testing.T, rec *httptest.ResponseRecorder) api.HealthResponseStatus {
	t.Helper()

	var body api.HealthResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Status
}

func TestLive_DoesNotTouchTheDatabase(t *testing.T) {
	h := health.New(pinger{err: errors.New("db is down")})
	rec := httptest.NewRecorder()

	h.Live(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, api.Ok, status(t, rec))
}

func TestReady(t *testing.T) {
	tests := []struct {
		name       string
		pingErr    error
		wantCode   int
		wantStatus api.HealthResponseStatus
	}{
		{"database answers", nil, http.StatusOK, api.Ok},
		{"database is down", errors.New("connection refused"), http.StatusServiceUnavailable, api.Unavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()

			health.New(pinger{err: tt.pingErr}).Ready(rec, httptest.NewRequest(http.MethodGet, "/ready", nil))

			require.Equal(t, tt.wantCode, rec.Code)
			require.Equal(t, tt.wantStatus, status(t, rec))
			require.NotContains(t, rec.Body.String(), "connection refused")
		})
	}
}
