package create_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/model"
	"github.com/Zuneve/template/internal/usecase/trip/create"
)

var (
	userID   = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	driverID = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	keyID    = uuid.MustParse("33333333-3333-3333-3333-333333333333")

	errBoom = errors.New("boom")
)

type fixture struct {
	trips       *MocktripRepository
	history     *MockhistoryRepository
	idempotency *MockidempotencyRepository
	tx          *MocktxManager
	metrics     *MocktripMetrics
	usecase     *create.Usecase
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	ctrl := gomock.NewController(t)
	f := &fixture{
		trips:       NewMocktripRepository(ctrl),
		history:     NewMockhistoryRepository(ctrl),
		idempotency: NewMockidempotencyRepository(ctrl),
		tx:          NewMocktxManager(ctrl),
		metrics:     NewMocktripMetrics(ctrl),
	}
	f.usecase = create.New(f.trips, f.history, f.idempotency, f.tx, f.metrics, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return f
}

// runTransactionsInline makes the fake TxManager call the callback itself, as
// a real one does, and report whatever the callback returned.
func (f *fixture) runTransactionsInline() {
	f.tx.EXPECT().Do(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		}).AnyTimes()
}

func validInput() create.Input {
	return create.Input{
		UserID:     userID,
		DriverID:   driverID,
		StartPoint: domain.Point{Latitude: 55.7, Longitude: 37.6},
		EndPoint:   domain.Point{Latitude: 55.8, Longitude: 37.7},
		Price:      500,
	}
}

func withKey(in create.Input) create.Input {
	key := keyID
	in.IdempotencyKey = &key
	return in
}

// claimedRecord returns the idempotency record the usecase stores for in. The
// request hash is internal to the usecase, so the test observes it instead of
// recomputing it.
func claimedRecord(t *testing.T, in create.Input) model.IdempotencyRecord {
	t.Helper()

	f := newFixture(t)
	f.runTransactionsInline()

	var record model.IdempotencyRecord
	f.idempotency.EXPECT().DeleteExpired(gomock.Any()).Return(nil)
	f.idempotency.EXPECT().Get(gomock.Any(), keyID).Return(model.IdempotencyRecord{}, domain.ErrIdempotencyKeyNotFound)
	f.idempotency.EXPECT().Insert(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, rec model.IdempotencyRecord) error {
			record = rec
			return nil
		})
	f.trips.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	f.history.EXPECT().AddStatusHistory(gomock.Any(), gomock.Any()).Return(nil)
	f.metrics.EXPECT().TripCreated(gomock.Any())

	_, err := f.usecase.Execute(t.Context(), in)
	require.NoError(t, err)
	require.NotEmpty(t, record.RequestHash)
	return record
}

func TestExecute_WithoutKey_CreatesTripAndHistory(t *testing.T) {
	f := newFixture(t)
	f.runTransactionsInline()

	var created domain.Trip
	f.trips.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, trip domain.Trip) error {
			created = trip
			return nil
		})
	f.history.EXPECT().AddStatusHistory(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, entry model.StatusHistoryEntry) error {
			require.Equal(t, created.ID, entry.TripID)
			require.Nil(t, entry.FromStatus)
			require.Equal(t, domain.StatusActive, entry.ToStatus)
			return nil
		})
	f.metrics.EXPECT().TripCreated(gomock.Any())

	out, err := f.usecase.Execute(t.Context(), validInput())

	require.NoError(t, err)
	require.True(t, out.Created)
	require.Equal(t, created, out.Trip)
	require.NotEqual(t, uuid.Nil, out.Trip.ID)
	require.Equal(t, domain.StatusActive, out.Trip.Status)
	require.Equal(t, userID, out.Trip.UserID)
	require.Equal(t, driverID, out.Trip.DriverID)
	require.Equal(t, int64(500), out.Trip.Price)
	require.False(t, out.Trip.StartedAt.IsZero())
}

func TestExecute_WithoutKey_Errors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fixture)
		want  error
	}{
		{
			name: "driver is busy",
			setup: func(f *fixture) {
				f.trips.EXPECT().Create(gomock.Any(), gomock.Any()).Return(domain.ErrDriverBusy)
			},
			want: domain.ErrDriverBusy,
		},
		{
			name: "trip insert fails",
			setup: func(f *fixture) {
				f.trips.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errBoom)
			},
			want: errBoom,
		},
		{
			name: "history insert fails",
			setup: func(f *fixture) {
				f.trips.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
				f.history.EXPECT().AddStatusHistory(gomock.Any(), gomock.Any()).Return(errBoom)
			},
			want: errBoom,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.runTransactionsInline()
			tt.setup(f)

			out, err := f.usecase.Execute(t.Context(), validInput())

			require.ErrorIs(t, err, tt.want)
			require.Equal(t, create.Output{}, out)
		})
	}
}

func TestExecute_TransactionFails(t *testing.T) {
	f := newFixture(t)
	f.tx.EXPECT().Do(gomock.Any(), gomock.Any()).Return(errBoom)

	_, err := f.usecase.Execute(t.Context(), validInput())

	require.ErrorIs(t, err, errBoom)
}

func TestExecute_InvalidInput_NothingIsTouched(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(in *create.Input)
		want   string
	}{
		{"no user", func(in *create.Input) { in.UserID = uuid.Nil }, "user_id is required"},
		{"no driver", func(in *create.Input) { in.DriverID = uuid.Nil }, "driver_id is required"},
		{"start out of range", func(in *create.Input) { in.StartPoint.Latitude = 91 }, "start_point is out of range"},
		{"end out of range", func(in *create.Input) { in.EndPoint.Longitude = -181 }, "end_point is out of range"},
		{"negative price", func(in *create.Input) { in.Price = -1 }, "price must be >= 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t) // no expectations: any call to a dependency fails the test
			in := validInput()
			tt.mutate(&in)

			_, err := f.usecase.Execute(t.Context(), in)

			var validationErr *domain.ValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Equal(t, tt.want, validationErr.Error())
		})
	}
}

func TestExecute_NewKey_ClaimsKeyAndCreatesTrip(t *testing.T) {
	f := newFixture(t)
	f.runTransactionsInline()

	var claimed model.IdempotencyRecord
	var order []string
	f.idempotency.EXPECT().DeleteExpired(gomock.Any()).Return(nil)
	f.idempotency.EXPECT().Get(gomock.Any(), keyID).Return(model.IdempotencyRecord{}, domain.ErrIdempotencyKeyNotFound)
	f.idempotency.EXPECT().Insert(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, rec model.IdempotencyRecord) error {
			order = append(order, "key")
			claimed = rec
			return nil
		})
	f.trips.EXPECT().Create(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, domain.Trip) error {
			order = append(order, "trip")
			return nil
		})
	f.history.EXPECT().AddStatusHistory(gomock.Any(), gomock.Any()).Return(nil)
	f.metrics.EXPECT().TripCreated(gomock.Any())

	out, err := f.usecase.Execute(t.Context(), withKey(validInput()))

	require.NoError(t, err)
	require.True(t, out.Created)
	require.Equal(t, []string{"key", "trip"}, order, "the key is claimed before the trip is inserted")
	require.Equal(t, keyID, claimed.Key)
	require.NotEmpty(t, claimed.RequestHash)
	require.Equal(t, out.Trip.ID, claimed.TripID)
}

func TestExecute_KnownKey(t *testing.T) {
	original := withKey(validInput())
	record := claimedRecord(t, original)

	tests := []struct {
		name     string
		in       create.Input
		tripErr  error
		wantErr  error
		wantTrip bool
	}{
		{name: "same body replays the stored trip", in: original, wantTrip: true},
		{name: "different price is a conflict", in: withPrice(original, 999), wantErr: domain.ErrIdempotencyKeyReused},
		{name: "different driver is a conflict", in: withDriver(original, uuid.New()), wantErr: domain.ErrIdempotencyKeyReused},
		{name: "different user is a conflict", in: changed(original, func(in *create.Input) { in.UserID = uuid.New() }), wantErr: domain.ErrIdempotencyKeyReused},
		{name: "different start latitude is a conflict", in: changed(original, func(in *create.Input) { in.StartPoint.Latitude = 1 }), wantErr: domain.ErrIdempotencyKeyReused},
		{name: "different start longitude is a conflict", in: changed(original, func(in *create.Input) { in.StartPoint.Longitude = 1 }), wantErr: domain.ErrIdempotencyKeyReused},
		{name: "different end latitude is a conflict", in: changed(original, func(in *create.Input) { in.EndPoint.Latitude = 1 }), wantErr: domain.ErrIdempotencyKeyReused},
		{name: "different end longitude is a conflict", in: changed(original, func(in *create.Input) { in.EndPoint.Longitude = 1 }), wantErr: domain.ErrIdempotencyKeyReused},
		{name: "start and end swapped is a conflict", in: changed(original, func(in *create.Input) { in.StartPoint, in.EndPoint = in.EndPoint, in.StartPoint }), wantErr: domain.ErrIdempotencyKeyReused},
		{name: "stored trip is gone", in: original, tripErr: domain.ErrTripNotFound, wantErr: domain.ErrTripNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t) // no transaction, no metrics: a replay creates nothing
			stored := domain.Trip{ID: record.TripID, UserID: userID, DriverID: driverID, Status: domain.StatusActive}

			f.idempotency.EXPECT().DeleteExpired(gomock.Any()).Return(nil)
			f.idempotency.EXPECT().Get(gomock.Any(), keyID).Return(record, nil)
			if tt.wantTrip || tt.tripErr != nil {
				f.trips.EXPECT().GetByID(gomock.Any(), record.TripID).Return(stored, tt.tripErr)
			}

			out, err := f.usecase.Execute(t.Context(), tt.in)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.False(t, out.Created)
			require.Equal(t, stored, out.Trip)
		})
	}
}

func TestExecute_KeyClaimedByConcurrentRequest(t *testing.T) {
	original := withKey(validInput())
	record := claimedRecord(t, original)

	tests := []struct {
		name    string
		in      create.Input
		getErr  error
		wantErr error
		replays bool
	}{
		{name: "same body replays the winner", in: original, replays: true},
		{name: "different body is a conflict", in: withPrice(original, 1), wantErr: domain.ErrIdempotencyKeyReused},
		{name: "winner disappeared", in: original, getErr: errBoom, wantErr: errBoom},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.runTransactionsInline()
			stored := domain.Trip{ID: record.TripID, Status: domain.StatusActive}

			f.idempotency.EXPECT().DeleteExpired(gomock.Any()).Return(nil)
			gomock.InOrder(
				f.idempotency.EXPECT().Get(gomock.Any(), keyID).Return(model.IdempotencyRecord{}, domain.ErrIdempotencyKeyNotFound),
				f.idempotency.EXPECT().Get(gomock.Any(), keyID).Return(record, tt.getErr),
			)
			f.idempotency.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(domain.ErrIdempotencyKeyConflict)
			if tt.replays {
				f.trips.EXPECT().GetByID(gomock.Any(), record.TripID).Return(stored, nil)
			}

			out, err := f.usecase.Execute(t.Context(), tt.in)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.False(t, out.Created)
			require.Equal(t, stored, out.Trip)
		})
	}
}

func TestExecute_WithKey_StorageErrors(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fixture)
		want  error
	}{
		{
			name: "cleanup of expired keys fails",
			setup: func(f *fixture) {
				f.idempotency.EXPECT().DeleteExpired(gomock.Any()).Return(errBoom)
			},
			want: errBoom,
		},
		{
			name: "key lookup fails",
			setup: func(f *fixture) {
				f.idempotency.EXPECT().DeleteExpired(gomock.Any()).Return(nil)
				f.idempotency.EXPECT().Get(gomock.Any(), keyID).Return(model.IdempotencyRecord{}, errBoom)
			},
			want: errBoom,
		},
		{
			name: "key insert fails",
			setup: func(f *fixture) {
				f.idempotency.EXPECT().DeleteExpired(gomock.Any()).Return(nil)
				f.idempotency.EXPECT().Get(gomock.Any(), keyID).Return(model.IdempotencyRecord{}, domain.ErrIdempotencyKeyNotFound)
				f.idempotency.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(errBoom)
			},
			want: errBoom,
		},
		{
			name: "driver is busy",
			setup: func(f *fixture) {
				f.idempotency.EXPECT().DeleteExpired(gomock.Any()).Return(nil)
				f.idempotency.EXPECT().Get(gomock.Any(), keyID).Return(model.IdempotencyRecord{}, domain.ErrIdempotencyKeyNotFound)
				f.idempotency.EXPECT().Insert(gomock.Any(), gomock.Any()).Return(nil)
				f.trips.EXPECT().Create(gomock.Any(), gomock.Any()).Return(domain.ErrDriverBusy)
			},
			want: domain.ErrDriverBusy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.runTransactionsInline()
			tt.setup(f)

			_, err := f.usecase.Execute(t.Context(), withKey(validInput()))

			require.ErrorIs(t, err, tt.want)
		})
	}
}

func withPrice(in create.Input, price int64) create.Input {
	in.Price = price
	return in
}

func changed(in create.Input, mutate func(*create.Input)) create.Input {
	mutate(&in)
	return in
}

func withDriver(in create.Input, id uuid.UUID) create.Input {
	in.DriverID = id
	return in
}
