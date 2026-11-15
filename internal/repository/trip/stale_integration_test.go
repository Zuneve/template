//go:build integration

package trip_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/testutil"
)

func TestListStaleTrips(t *testing.T) {
	r, pool := setup(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	staleBefore := now.Add(-time.Minute)

	// create stores a trip started at startedAt; lastPoint, when set, is its newest point.
	create := func(startedAt time.Time, lastPoint *time.Time) domain.Trip {
		tr := newTrip(t)
		tr.StartedAt = startedAt
		testutil.CleanupTrips(t, pool, tr.ID)
		require.NoError(t, r.Create(ctx, tr))
		if lastPoint != nil {
			_, err := r.AddPosition(ctx, domain.Position{TripID: tr.ID, RecordedAt: *lastPoint})
			require.NoError(t, err)
			require.NoError(t, r.TouchLastPosition(ctx, tr.ID, *lastPoint))
		}
		return tr
	}
	ago := func(d time.Duration) *time.Time { at := now.Add(-d); return &at }

	silentFor10m := create(now.Add(-time.Hour), ago(10*time.Minute))
	silentFor5m := create(now.Add(-time.Hour), ago(5*time.Minute))
	neverTrackedStartedAgo30m := create(*ago(30 * time.Minute), nil)
	freshPoint := create(now.Add(-time.Hour), ago(10*time.Second))
	justStarted := create(now.Add(-10*time.Second), nil)
	onTheEdge := create(now.Add(-time.Hour), &staleBefore)
	oldStartButFreshPoint := create(*ago(24 * time.Hour), ago(5*time.Second))
	completed := create(now.Add(-time.Hour), ago(10*time.Minute))
	require.NoError(t, completed.Finish(now))
	require.NoError(t, r.Update(ctx, completed))

	got, err := r.ListStaleTrips(ctx, staleBefore)
	require.NoError(t, err)

	// The database may hold other active trips; look only at ours.
	ours := map[uuid.UUID]string{
		silentFor10m.ID:              "silentFor10m",
		silentFor5m.ID:               "silentFor5m",
		neverTrackedStartedAgo30m.ID: "neverTrackedStartedAgo30m",
		freshPoint.ID:                "freshPoint",
		justStarted.ID:               "justStarted",
		onTheEdge.ID:                 "onTheEdge",
		oldStartButFreshPoint.ID:     "oldStartButFreshPoint",
		completed.ID:                 "completed",
	}
	var names []string
	for _, st := range got {
		if name, ok := ours[st.TripID]; ok {
			names = append(names, name)
		}
	}

	// The 30-minute-old start is older than both silences, so it goes first.
	require.Equal(t, []string{"neverTrackedStartedAgo30m", "silentFor10m", "silentFor5m"}, names)

	// The driver comes along: it is the push recipient.
	for _, st := range got {
		if st.TripID == silentFor10m.ID {
			require.Equal(t, silentFor10m.DriverID, st.DriverID)
		}
	}
}

func TestListStaleTrips_NothingStaleIsEmptySlice(t *testing.T) {
	r, _ := setup(t)

	got, err := r.ListStaleTrips(context.Background(), time.Unix(0, 0))

	require.NoError(t, err)
	require.NotNil(t, got)
}
