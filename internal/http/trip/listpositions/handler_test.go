package listpositions_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domain "github.com/Zuneve/template/internal/domain/trip"
	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/trip/listpositions"
)

var tripID = uuid.MustParse("66666666-6666-6666-6666-666666666666")

func serve(t *testing.T, setup func(uc *Mockusecase)) *httptest.ResponseRecorder {
	t.Helper()

	uc := NewMockusecase(gomock.NewController(t))
	setup(uc)
	h := listpositions.New(uc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/trips/"+tripID.String()+"/positions", nil)
	rec := httptest.NewRecorder()
	h.Handle(rec, req, tripID)
	return rec
}

func TestHandle_OK(t *testing.T) {
	first := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	rec := serve(t, func(uc *Mockusecase) {
		uc.EXPECT().Execute(gomock.Any(), tripID).Return([]domain.Position{
			{ID: 1, TripID: tripID, Point: domain.Point{Latitude: 1, Longitude: 2}, RecordedAt: first},
			{ID: 2, TripID: tripID, Point: domain.Point{Latitude: 3, Longitude: 4}, RecordedAt: first.Add(time.Minute)},
		}, nil)
	})

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var got api.TripPositionsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Positions, 2)
	require.Equal(t, int64(1), got.Positions[0].Id)
	require.Equal(t, int64(2), got.Positions[1].Id)
	require.True(t, got.Positions[1].RecordedAt.Equal(first.Add(time.Minute)))
}

func TestHandle_EmptyRouteIsEmptyArray(t *testing.T) {
	rec := serve(t, func(uc *Mockusecase) {
		uc.EXPECT().Execute(gomock.Any(), tripID).Return([]domain.Position{}, nil)
	})

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"positions":[]}`, rec.Body.String())
}

func TestHandle_Errors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"trip not found", domain.ErrTripNotFound, http.StatusNotFound, "trip_not_found"},
		{"unexpected", errors.New("pq: connection refused at 10.0.0.5"), http.StatusInternalServerError, "internal_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, func(uc *Mockusecase) {
				uc.EXPECT().Execute(gomock.Any(), tripID).Return(nil, tt.err)
			})

			require.Equal(t, tt.wantStatus, rec.Code)
			require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
			var p api.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			require.Equal(t, tt.wantCode, p.Code)
			require.NotContains(t, rec.Body.String(), "10.0.0.5")
		})
	}
}
