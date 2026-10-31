package domainerror_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/http/domainerror"
)

func TestProblem(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantDetail string
	}{
		{
			name:       "trip not found",
			err:        domain.ErrTripNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   "trip_not_found",
			wantDetail: "Trip was not found",
		},
		{
			name:       "trip completed",
			err:        domain.ErrTripCompleted,
			wantStatus: http.StatusConflict,
			wantCode:   "trip_completed",
			wantDetail: "Operation is not allowed for a completed trip",
		},
		{
			name:       "driver busy",
			err:        domain.ErrDriverBusy,
			wantStatus: http.StatusConflict,
			wantCode:   "driver_busy",
			wantDetail: "Driver already has an active trip",
		},
		{
			name:       "idempotency key reused with another body",
			err:        domain.ErrIdempotencyKeyReused,
			wantStatus: http.StatusConflict,
			wantCode:   "idempotency_conflict",
			wantDetail: "Idempotency-Key was already used with a different request body",
		},
		{
			name:       "validation error carries its own detail",
			err:        &domain.ValidationError{Field: "price", Reason: "must be >= 0"},
			wantStatus: http.StatusBadRequest,
			wantCode:   "invalid_request",
			wantDetail: "price must be >= 0",
		},
		{
			name:       "wrapped domain error is still recognised",
			err:        fmt.Errorf("get replayed trip: %w", domain.ErrTripNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   "trip_not_found",
			wantDetail: "Trip was not found",
		},
		{
			name:       "unknown error is internal",
			err:        errors.New("pq: password authentication failed for user tripgo"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_error",
			wantDetail: "Internal server error",
		},
		{
			name:       "wrapped infrastructure error is internal",
			err:        fmt.Errorf("insert trip: %w", errors.New("connection refused")),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "internal_error",
			wantDetail: "Internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := domainerror.Problem(tt.err)

			require.Equal(t, tt.wantStatus, status)
			require.EqualValues(t, tt.wantStatus, body.Status)
			require.Equal(t, tt.wantCode, body.Code)
			require.NotNil(t, body.Detail)
			require.Equal(t, tt.wantDetail, *body.Detail)
			require.NotEmpty(t, body.Title)
			require.Contains(t, body.Type, "https://tripgo.example/problems/")
		})
	}
}

func TestProblem_InternalErrorNeverLeaksDetails(t *testing.T) {
	leak := "connect to db:5432 as tripgo failed"

	_, body := domainerror.Problem(errors.New("dial " + leak))

	require.NotContains(t, *body.Detail, leak)
	require.NotContains(t, body.Title, leak)
}

func TestInvalidRequest(t *testing.T) {
	status, body := domainerror.InvalidRequest("price is required")

	require.Equal(t, http.StatusBadRequest, status)
	require.Equal(t, "invalid_request", body.Code)
	require.Equal(t, "price is required", *body.Detail)
}
