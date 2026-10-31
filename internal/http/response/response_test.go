package response_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	domain "github.com/Zuneve/template/internal/domain/trip"
	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/response"
)

func TestJSON(t *testing.T) {
	rec := httptest.NewRecorder()

	response.JSON(rec, http.StatusCreated, map[string]int{"a": 1})

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"a":1}`, rec.Body.String())
}

func TestInvalidRequest(t *testing.T) {
	rec := httptest.NewRecorder()

	response.InvalidRequest(rec, httptest.NewRequest(http.MethodGet, "/api/v1/trips/x", nil), "bad id")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	var p api.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	require.Equal(t, "invalid_request", p.Code)
	require.Equal(t, "bad id", *p.Detail)
	require.Equal(t, "/api/v1/trips/x", *p.Instance)
}

func TestError_LogsOnlyInternalErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantLogged bool
	}{
		{"not found is the client's matter", domain.ErrTripNotFound, http.StatusNotFound, false},
		{"unknown error is ours", errors.New("db exploded"), http.StatusInternalServerError, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, nil))
			rec := httptest.NewRecorder()

			response.Error(rec, httptest.NewRequest(http.MethodGet, "/x", nil), logger, tt.err)

			require.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantLogged {
				require.Contains(t, buf.String(), "db exploded")
				require.NotContains(t, rec.Body.String(), "db exploded")
			} else {
				require.Empty(t, buf.String())
			}
		})
	}
}
