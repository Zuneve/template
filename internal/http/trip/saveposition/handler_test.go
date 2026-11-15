package saveposition_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domain "github.com/Zuneve/template/internal/domain/trip"
	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/trip/saveposition"
	saveusecase "github.com/Zuneve/template/internal/usecase/trip/saveposition"
)

var tripID = uuid.MustParse("66666666-6666-6666-6666-666666666666")

const validBody = `{"coordinates":{"latitude":59.935,"longitude":30.335},"recorded_at":"2026-08-24T12:00:05Z"}`

// serve sends body to the handler. A nil setup means the usecase must not be
// called at all: the mock is strict.
func serve(t *testing.T, body string, setup func(uc *Mockusecase)) *httptest.ResponseRecorder {
	t.Helper()

	uc := NewMockusecase(gomock.NewController(t))
	if setup != nil {
		setup(uc)
	}
	h := saveposition.New(uc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/trips/"+tripID.String()+"/positions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Handle(rec, req, tripID)
	return rec
}

func problem(t *testing.T, rec *httptest.ResponseRecorder) api.Problem {
	t.Helper()

	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	var p api.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	return p
}

func TestHandle_Created(t *testing.T) {
	recordedAt := time.Date(2026, 8, 24, 12, 0, 5, 0, time.UTC)
	point := domain.Point{Latitude: 59.935, Longitude: 30.335}

	rec := serve(t, validBody, func(uc *Mockusecase) {
		uc.EXPECT().Execute(gomock.Any(), saveusecase.Input{TripID: tripID, Point: point, RecordedAt: recordedAt}).
			Return(domain.Position{ID: 42, TripID: tripID, Point: point, RecordedAt: recordedAt}, nil)
	})

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var got api.TripPosition
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, int64(42), got.Id)
	require.Equal(t, tripID, got.TripId)
	require.InDelta(t, 59.935, got.Coordinates.Latitude, 0)
	require.InDelta(t, 30.335, got.Coordinates.Longitude, 0)
	require.True(t, got.RecordedAt.Equal(recordedAt))
}

// 0,0 is a real place in the Gulf of Guinea; a zero must not be mistaken for a missing field.
func TestHandle_ZeroCoordinatesAreValid(t *testing.T) {
	rec := serve(t, `{"coordinates":{"latitude":0,"longitude":0},"recorded_at":"2026-08-24T12:00:05Z"}`, func(uc *Mockusecase) {
		uc.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(domain.Position{ID: 1}, nil)
	})

	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestHandle_BadBody(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not json", `{`},
		{"empty body", ``},
		{"json array", `[]`},
		{"coordinates missing", `{"recorded_at":"2026-08-24T12:00:05Z"}`},
		{"coordinates null", `{"coordinates":null,"recorded_at":"2026-08-24T12:00:05Z"}`},
		{"coordinates not an object", `{"coordinates":5,"recorded_at":"2026-08-24T12:00:05Z"}`},
		{"latitude missing", `{"coordinates":{"longitude":30},"recorded_at":"2026-08-24T12:00:05Z"}`},
		{"longitude missing", `{"coordinates":{"latitude":59},"recorded_at":"2026-08-24T12:00:05Z"}`},
		{"latitude is a string", `{"coordinates":{"latitude":"59","longitude":30},"recorded_at":"2026-08-24T12:00:05Z"}`},
		{"recorded_at missing", `{"coordinates":{"latitude":59,"longitude":30}}`},
		{"recorded_at null", `{"coordinates":{"latitude":59,"longitude":30},"recorded_at":null}`},
		{"recorded_at does not parse", `{"coordinates":{"latitude":59,"longitude":30},"recorded_at":"yesterday"}`},
		{"recorded_at is a number", `{"coordinates":{"latitude":59,"longitude":30},"recorded_at":1756036805}`},
		{"unknown field", `{"coordinates":{"latitude":59,"longitude":30},"recorded_at":"2026-08-24T12:00:05Z","speed":3}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, tt.body, nil)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, "invalid_request", problem(t, rec).Code)
		})
	}
}

func TestHandle_UsecaseErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"validation", &domain.ValidationError{Field: "coordinates", Reason: "are out of range"}, http.StatusBadRequest, "invalid_request"},
		{"trip not found", domain.ErrTripNotFound, http.StatusNotFound, "trip_not_found"},
		{"trip completed", domain.ErrTripCompleted, http.StatusConflict, "trip_completed"},
		{"unexpected", errors.New("pq: connection refused at 10.0.0.5"), http.StatusInternalServerError, "internal_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, validBody, func(uc *Mockusecase) {
				uc.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(domain.Position{}, tt.err)
			})

			require.Equal(t, tt.wantStatus, rec.Code)
			p := problem(t, rec)
			require.Equal(t, tt.wantCode, p.Code)
			require.NotContains(t, rec.Body.String(), "10.0.0.5", "internal error text must not reach the client")
		})
	}
}
