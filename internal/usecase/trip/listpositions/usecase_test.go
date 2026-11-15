package listpositions_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/usecase/trip/listpositions"
)

var (
	tripID  = uuid.MustParse("55555555-5555-5555-5555-555555555555")
	errBoom = errors.New("boom")
)

func TestExecute(t *testing.T) {
	route := []domain.Position{{ID: 1, TripID: tripID}, {ID: 2, TripID: tripID}}

	t.Run("returns the route of an existing trip", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		trips, positions := NewMocktripRepository(ctrl), NewMockpositionRepository(ctrl)
		gomock.InOrder(
			trips.EXPECT().GetByID(gomock.Any(), tripID).Return(domain.Trip{ID: tripID}, nil),
			positions.EXPECT().ListPositions(gomock.Any(), tripID).Return(route, nil),
		)

		got, err := listpositions.New(trips, positions).Execute(t.Context(), tripID)

		require.NoError(t, err)
		require.Equal(t, route, got)
	})

	t.Run("unknown trip does not read positions", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		trips, positions := NewMocktripRepository(ctrl), NewMockpositionRepository(ctrl)
		trips.EXPECT().GetByID(gomock.Any(), tripID).Return(domain.Trip{}, domain.ErrTripNotFound)

		got, err := listpositions.New(trips, positions).Execute(t.Context(), tripID)

		require.ErrorIs(t, err, domain.ErrTripNotFound)
		require.Nil(t, got)
	})

	t.Run("positions read error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		trips, positions := NewMocktripRepository(ctrl), NewMockpositionRepository(ctrl)
		trips.EXPECT().GetByID(gomock.Any(), tripID).Return(domain.Trip{ID: tripID}, nil)
		positions.EXPECT().ListPositions(gomock.Any(), tripID).Return(nil, errBoom)

		_, err := listpositions.New(trips, positions).Execute(t.Context(), tripID)

		require.ErrorIs(t, err, errBoom)
	})
}
