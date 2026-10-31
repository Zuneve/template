package create_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domain "github.com/Zuneve/template/internal/domain/trip"
	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/trip/create"
	createusecase "github.com/Zuneve/template/internal/usecase/trip/create"
)

const invalidJSON = "request body is not valid JSON"

const validBody = `{
	"user_id": "11111111-1111-1111-1111-111111111111",
	"driver_id": "22222222-2222-2222-2222-222222222222",
	"start_point": {"latitude": 55.7, "longitude": 37.6},
	"end_point": {"latitude": 55.8, "longitude": 37.7},
	"price": 500
}`

var (
	userID   = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	driverID = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	tripID   = uuid.MustParse("66666666-6666-6666-6666-666666666666")
)

func newHandler(t *testing.T) (*create.Handler, *Mockusecase) {
	t.Helper()

	uc := NewMockusecase(gomock.NewController(t))
	return create.New(uc, slog.New(slog.NewTextHandler(io.Discard, nil))), uc
}

func serve(h *create.Handler, body string, params api.CreateTripParams) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Handle(rec, req, params)
	return rec
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) api.Problem {
	t.Helper()

	require.Equal(t, "application/problem+json", rec.Header().Get("Content-Type"))
	var p api.Problem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	return p
}

func storedTrip() domain.Trip {
	return domain.Trip{
		ID:         tripID,
		UserID:     userID,
		DriverID:   driverID,
		StartPoint: domain.Point{Latitude: 55.7, Longitude: 37.6},
		EndPoint:   domain.Point{Latitude: 55.8, Longitude: 37.7},
		Price:      500,
		Status:     domain.StatusActive,
	}
}

func TestHandle_Created(t *testing.T) {
	h, uc := newHandler(t)
	uc.EXPECT().Execute(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, in createusecase.Input) (createusecase.Output, error) {
			require.Equal(t, createusecase.Input{
				UserID:     userID,
				DriverID:   driverID,
				StartPoint: domain.Point{Latitude: 55.7, Longitude: 37.6},
				EndPoint:   domain.Point{Latitude: 55.8, Longitude: 37.7},
				Price:      500,
			}, in)
			return createusecase.Output{Trip: storedTrip(), Created: true}, nil
		})

	rec := serve(h, validBody, api.CreateTripParams{})

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "/api/v1/trips/"+tripID.String(), rec.Header().Get("Location"))
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var got api.Trip
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, tripID, got.Id)
	require.Equal(t, api.TripStatus("active"), got.Status)
	require.Equal(t, int64(500), got.Price)
}

func TestHandle_Replay_Is200WithoutLocation(t *testing.T) {
	key := uuid.MustParse("77777777-7777-7777-7777-777777777777")
	h, uc := newHandler(t)
	uc.EXPECT().Execute(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, in createusecase.Input) (createusecase.Output, error) {
			require.NotNil(t, in.IdempotencyKey)
			require.Equal(t, key, *in.IdempotencyKey)
			return createusecase.Output{Trip: storedTrip(), Created: false}, nil
		})

	rec := serve(h, validBody, api.CreateTripParams{IdempotencyKey: &key})

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Header().Get("Location"))
}

func TestHandle_InvalidBody_Is400WithoutCallingUsecase(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantDetail string
	}{
		{"empty body", ``, invalidJSON},
		{"not json", `{`, invalidJSON},
		{"json array", `[]`, invalidJSON},
		{"empty object", `{}`, "user_id is required"},
		{"null user", strings.Replace(validBody, `"11111111-1111-1111-1111-111111111111"`, `null`, 1), "user_id is required"},
		{"no price", strings.Replace(validBody, `"price": 500`, `"x": 1`, 1), "price is required"},
		{"no end point", strings.Replace(validBody, `"end_point": {"latitude": 55.8, "longitude": 37.7},`, ``, 1), "end_point is required"},
		{"start point is not an object", strings.Replace(validBody, `{"latitude": 55.7, "longitude": 37.6}`, `1`, 1), "start_point must be an object"},
		{"no latitude", strings.Replace(validBody, `"latitude": 55.7, `, ``, 1), "start_point.latitude is required"},
		{"no longitude", strings.Replace(validBody, `, "longitude": 37.7`, ``, 1), "end_point.longitude is required"},
		{"user is not a uuid", strings.Replace(validBody, `11111111-1111-1111-1111-111111111111`, `nope`, 1), invalidJSON},
		{"price is a string", strings.Replace(validBody, `500`, `"500"`, 1), invalidJSON},
		{"unknown field", strings.Replace(validBody, `"price": 500`, `"price": 500, "tip": 1`, 1), invalidJSON},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newHandler(t) // no expectations: the usecase must not be called

			rec := serve(h, tt.body, api.CreateTripParams{})

			require.Equal(t, http.StatusBadRequest, rec.Code)
			p := decodeProblem(t, rec)
			require.Equal(t, "invalid_request", p.Code)
			require.Contains(t, *p.Detail, tt.wantDetail)
		})
	}
}

func TestHandle_TooLargeBody_Is400(t *testing.T) {
	h, _ := newHandler(t)

	rec := serve(h, `{"pad": "`+strings.Repeat("x", 2<<20)+`"}`, api.CreateTripParams{})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "invalid_request", decodeProblem(t, rec).Code)
}

func TestHandle_UsecaseErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"driver busy", domain.ErrDriverBusy, http.StatusConflict, "driver_busy"},
		{"key reused", domain.ErrIdempotencyKeyReused, http.StatusConflict, "idempotency_conflict"},
		{"domain validation", &domain.ValidationError{Field: "price", Reason: "must be >= 0"}, http.StatusBadRequest, "invalid_request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, uc := newHandler(t)
			uc.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(createusecase.Output{}, tt.err)

			rec := serve(h, validBody, api.CreateTripParams{})

			require.Equal(t, tt.wantStatus, rec.Code)
			p := decodeProblem(t, rec)
			require.Equal(t, tt.wantCode, p.Code)
			require.Equal(t, "/api/v1/trips", *p.Instance)
			require.Empty(t, rec.Header().Get("Location"))
		})
	}
}

func TestHandle_RepositoryError_Is500WithoutDetails(t *testing.T) {
	const secret = "password authentication failed for user tripgo"

	h, uc := newHandler(t)
	uc.EXPECT().Execute(gomock.Any(), gomock.Any()).
		Return(createusecase.Output{}, errors.New("insert trip: "+secret))

	rec := serve(h, validBody, api.CreateTripParams{})

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	p := decodeProblem(t, rec)
	require.Equal(t, "internal_error", p.Code)
	require.Equal(t, "Internal server error", *p.Detail)
	require.NotContains(t, rec.Body.String(), secret)
	require.NotContains(t, rec.Body.String(), "insert trip")
}
