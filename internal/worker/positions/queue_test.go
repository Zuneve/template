package positions_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Zuneve/template/internal/worker/positions"
)

func TestQueue_Enqueue(t *testing.T) {
	q := positions.NewQueue(2)

	require.Equal(t, positions.ResultQueued, q.Enqueue(t.Context(), staleTrip(1)))
	require.Equal(t, positions.ResultDuplicate, q.Enqueue(t.Context(), staleTrip(1)), "a queued trip is not queued twice")
	require.Equal(t, positions.ResultQueued, q.Enqueue(t.Context(), staleTrip(2)))
	require.Equal(t, positions.ResultDropped, q.Enqueue(t.Context(), staleTrip(3)), "a full queue drops instead of blocking")
	require.Equal(t, 2, q.Len())
}

func TestQueue_CancelOfUnknownTripIsNoop(t *testing.T) {
	q := positions.NewQueue(1)

	q.Cancel(staleTrip(1).TripID)

	require.Equal(t, positions.ResultQueued, q.Enqueue(t.Context(), staleTrip(1)))
}

func TestQueue_ClosedQueueDropsAndCloseIsIdempotent(t *testing.T) {
	q := positions.NewQueue(1)

	q.Close()
	q.Close()

	require.Equal(t, positions.ResultDropped, q.Enqueue(t.Context(), staleTrip(1)))
}

// Run with -race: the scanner, the workers and the position handlers touch the
// queue at the same time.
func TestQueue_ConcurrentUse(t *testing.T) {
	q := positions.NewQueue(50)
	var wg sync.WaitGroup

	for g := range 8 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := range 200 {
				q.Enqueue(t.Context(), staleTrip(g*1000+i%20))
			}
		}()
		go func() {
			defer wg.Done()
			for i := range 200 {
				q.Cancel(staleTrip(g*1000 + i%20).TripID)
			}
		}()
	}
	wg.Wait()

	require.LessOrEqual(t, q.Len(), 50)
}
