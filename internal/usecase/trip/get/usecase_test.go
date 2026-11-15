package get_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/usecase/trip/get"
)

var tripID = uuid.MustParse("55555555-5555-5555-5555-555555555555")

func TestExecute(t *testing.T) {
	stored := domain.Trip{ID: tripID, Status: domain.StatusActive}

	tests := []struct {
		name    string
		trip    domain.Trip
		repoErr error
		wantErr error
	}{
		{name: "returns the stored trip", trip: stored},
		{name: "trip does not exist", repoErr: domain.ErrTripNotFound, wantErr: domain.ErrTripNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trips := NewMocktripRepository(gomock.NewController(t))
			trips.EXPECT().GetByID(gomock.Any(), tripID).Return(tt.trip, tt.repoErr)

			trip, err := get.New(trips).Execute(t.Context(), tripID)

			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.trip, trip)
		})
	}
}
