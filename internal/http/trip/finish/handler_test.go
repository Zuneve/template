package finish_test

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domain "github.com/Zuneve/template/internal/domain/trip"
	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/trip/finish"
)

var tripID = uuid.MustParse("66666666-6666-6666-6666-666666666666")

func serve(t *testing.T, setup func(uc *Mockusecase)) *httptest.ResponseRecorder {
	t.Helper()

	uc := NewMockusecase(gomock.NewController(t))
	setup(uc)
	h := finish.New(uc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	path := "/api/v1/trips/" + tripID.String()
	req := httptest.NewRequest(http.MethodPost, path+"/finish", nil)
	rec := httptest.NewRecorder()
	h.Handle(rec, req, tripID)
	return rec
}

func TestHandle_OK(t *testing.T) {
	rec := serve(t, func(uc *Mockusecase) {
		uc.EXPECT().Execute(gomock.Any(), tripID).Return(domain.Trip{ID: tripID, Status: domain.StatusCompleted}, nil)
	})

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var got api.Trip
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, tripID, got.Id)
	require.Equal(t, api.TripStatus("completed"), got.Status)
}

func TestHandle_Errors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"trip not found", domain.ErrTripNotFound, http.StatusNotFound, "trip_not_found"},
		{"trip already completed", domain.ErrTripCompleted, http.StatusConflict, "trip_completed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, func(uc *Mockusecase) {
				uc.EXPECT().Execute(gomock.Any(), tripID).Return(domain.Trip{}, tt.err)
			})

			require.Equal(t, tt.wantStatus, rec.Code)
			require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
			var p api.Problem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
			require.Equal(t, tt.wantCode, p.Code)
			require.Equal(t, "/api/v1/trips/"+tripID.String()+"/finish", *p.Instance)
		})
	}
}

func TestHandle_RepositoryError_Is500WithoutDetails(t *testing.T) {
	const secret = "connection refused to 10.0.0.5:5432"

	rec := serve(t, func(uc *Mockusecase) {
		uc.EXPECT().Execute(gomock.Any(), tripID).Return(domain.Trip{}, errors.New("query trip: "+secret))
	})

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	var p api.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	require.Equal(t, "internal_error", p.Code)
	require.NotContains(t, rec.Body.String(), secret)
	require.NotContains(t, rec.Body.String(), "query trip")
}
