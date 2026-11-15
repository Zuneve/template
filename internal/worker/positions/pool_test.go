package positions_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"

	"github.com/Zuneve/template/internal/worker/positions"
)

const requestTimeout = time.Second

type poolEnv struct {
	pusher  *Mockpusher
	metrics *MockpoolMetrics
	queue   *positions.Queue
}

func newPoolEnv(t *testing.T) *poolEnv {
	t.Helper()

	ctrl := gomock.NewController(t)
	return &poolEnv{
		pusher:  NewMockpusher(ctrl),
		metrics: NewMockpoolMetrics(ctrl),
		queue:   positions.NewQueue(10),
	}
}

func (e *poolEnv) pool(workers int, timeout time.Duration) *positions.Pool {
	return positions.NewPool(positions.PoolParams{
		Queue:          e.queue,
		Pusher:         e.pusher,
		Metrics:        e.metrics,
		Workers:        workers,
		RequestTimeout: timeout,
		Logger:         discardLogger(),
	})
}

// drain shuts the pool down gracefully: workers finish whatever is queued.
func drain(t *testing.T, p *positions.Pool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
	defer cancel()
	require.NoError(t, p.Shutdown(ctx))
}

func TestPool_PushesQueuedTripsToTheirDrivers(t *testing.T) {
	e := newPoolEnv(t)
	for n := 1; n <= 3; n++ {
		require.Equal(t, positions.ResultQueued, e.queue.Enqueue(t.Context(), staleTrip(n)))
	}
	var mu sync.Mutex
	pushed := map[uuid.UUID]uuid.UUID{}
	e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, driverID, tripID uuid.UUID) error {
			mu.Lock()
			defer mu.Unlock()
			pushed[tripID] = driverID
			return nil
		}).Times(3)
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "success", gomock.Any()).Times(3)
	p := e.pool(2, requestTimeout)
	p.Start(t.Context())

	drain(t, p)

	require.Equal(t, map[uuid.UUID]uuid.UUID{
		staleTrip(1).TripID: staleTrip(1).DriverID,
		staleTrip(2).TripID: staleTrip(2).DriverID,
		staleTrip(3).TripID: staleTrip(3).DriverID,
	}, pushed)
}

func TestPool_RunsTasksWithConfiguredNumberOfWorkers(t *testing.T) {
	const workers = 3
	e := newPoolEnv(t)
	for n := 1; n <= workers+2; n++ {
		e.queue.Enqueue(t.Context(), staleTrip(n))
	}
	var inFlight, maxInFlight, done atomic.Int32
	started := make(chan struct{}, workers+2)
	release := make(chan struct{})
	e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, uuid.UUID, uuid.UUID) error {
			n := inFlight.Add(1)
			for {
				prev := maxInFlight.Load()
				if n <= prev || maxInFlight.CompareAndSwap(prev, n) {
					break
				}
			}
			started <- struct{}{}
			<-release
			inFlight.Add(-1)
			done.Add(1)
			return nil
		}).Times(workers + 2)
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "success", gomock.Any()).Times(workers + 2)
	p := e.pool(workers, requestTimeout)
	p.Start(t.Context())

	for range workers {
		receive(t, started)
	}
	// All workers are busy: the remaining two tasks wait in the queue.
	require.Empty(t, started)
	require.EqualValues(t, workers, inFlight.Load())

	close(release)
	drain(t, p)

	require.EqualValues(t, workers, maxInFlight.Load(), "never more parallel calls than workers")
	require.EqualValues(t, workers+2, done.Load())
}

func TestPool_PositionArrivingAfterQueueingCancelsThePush(t *testing.T) {
	e := newPoolEnv(t)
	require.Equal(t, positions.ResultQueued, e.queue.Enqueue(t.Context(), staleTrip(1)))
	require.Equal(t, positions.ResultQueued, e.queue.Enqueue(t.Context(), staleTrip(2)))
	e.queue.Cancel(staleTrip(1).TripID)
	// Only trip 2 may reach the Push Service; the mock fails on anything else.
	e.pusher.EXPECT().RequestPosition(gomock.Any(), staleTrip(2).DriverID, staleTrip(2).TripID).Return(nil)
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "success", gomock.Any())
	p := e.pool(1, requestTimeout)
	p.Start(t.Context())

	drain(t, p)
}

func TestPool_TripCancelledAndQueuedAgainIsPushedOnce(t *testing.T) {
	e := newPoolEnv(t)
	require.Equal(t, positions.ResultQueued, e.queue.Enqueue(t.Context(), staleTrip(1)))
	e.queue.Cancel(staleTrip(1).TripID)
	// The trip went silent again; its old task is still in the channel.
	require.Equal(t, positions.ResultQueued, e.queue.Enqueue(t.Context(), staleTrip(1)))
	e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), staleTrip(1).TripID).Return(nil).Times(1)
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "success", gomock.Any()).Times(1)
	p := e.pool(2, requestTimeout)
	p.Start(t.Context())

	drain(t, p)
}

func TestPool_TripIsDuplicateWhileBeingProcessed(t *testing.T) {
	e := newPoolEnv(t)
	e.queue.Enqueue(t.Context(), staleTrip(1))
	inFlight := make(chan struct{})
	release := make(chan struct{})
	e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, uuid.UUID, uuid.UUID) error {
			close(inFlight)
			<-release
			return nil
		})
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "success", gomock.Any())
	p := e.pool(1, requestTimeout)
	p.Start(t.Context())
	receive(t, inFlight)

	require.Equal(t, positions.ResultDuplicate, e.queue.Enqueue(t.Context(), staleTrip(1)), "the trip is still being handled")

	close(release)
	drain(t, p)
}

func TestPool_PushFailureDoesNotStopTheWorker(t *testing.T) {
	e := newPoolEnv(t)
	e.queue.Enqueue(t.Context(), staleTrip(1))
	e.queue.Enqueue(t.Context(), staleTrip(2))
	gomock.InOrder(
		e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), staleTrip(1).TripID).Return(errors.New("push service is down")),
		e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), staleTrip(2).TripID).Return(nil),
	)
	gomock.InOrder(
		e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "error", gomock.Any()),
		e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "success", gomock.Any()),
	)
	// One worker handles both tasks, so it survived the first failure.
	p := e.pool(1, requestTimeout)
	p.Start(t.Context())

	drain(t, p)
}

func TestPool_EachPushHasATimeout(t *testing.T) {
	e := newPoolEnv(t)
	e.queue.Enqueue(t.Context(), staleTrip(1))
	e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _, _ uuid.UUID) error {
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "the push context must have a deadline")
			require.LessOrEqual(t, time.Until(deadline), requestTimeout)
			return nil
		})
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "success", gomock.Any())
	p := e.pool(1, requestTimeout)
	p.Start(t.Context())

	drain(t, p)
}

func TestPool_SlowPushIsCutOffByTheTimeout(t *testing.T) {
	e := newPoolEnv(t)
	e.queue.Enqueue(t.Context(), staleTrip(1))
	e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _, _ uuid.UUID) error {
			<-ctx.Done()
			return ctx.Err()
		})
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "error", gomock.Any())
	p := e.pool(1, 20*time.Millisecond)
	p.Start(t.Context())

	drain(t, p)
}

func TestPool_ShutdownPastItsDeadlineCancelsPushesAndStopsWorkers(t *testing.T) {
	e := newPoolEnv(t)
	e.queue.Enqueue(t.Context(), staleTrip(1))
	e.queue.Enqueue(t.Context(), staleTrip(2))
	started := make(chan struct{})
	e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _, _ uuid.UUID) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}).Times(1)
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "error", gomock.Any())
	p := e.pool(1, time.Hour)
	p.Start(t.Context())
	receive(t, started)

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.Shutdown(expired)

	// Trip 2 is abandoned, and no worker is left behind (goleak checks that).
	require.ErrorIs(t, err, context.Canceled)
}

func TestPool_ContextCancellationStopsWorkersAndAbortsPushes(t *testing.T) {
	e := newPoolEnv(t)
	e.queue.Enqueue(t.Context(), staleTrip(1))
	started := make(chan struct{})
	e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _, _ uuid.UUID) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "error", gomock.Any())
	ctx, cancel := context.WithCancel(context.Background())
	p := e.pool(2, time.Hour)
	p.Start(ctx)
	receive(t, started)

	cancel()

	// The workers have already been told to leave, so Shutdown has nothing to wait for.
	drain(t, p)
}

func TestPool_QueueIsClosedAfterShutdown(t *testing.T) {
	e := newPoolEnv(t)
	p := e.pool(1, requestTimeout)
	p.Start(t.Context())

	drain(t, p)

	require.Equal(t, positions.ResultDropped, e.queue.Enqueue(t.Context(), staleTrip(1)))
}

func (e *poolEnv) tracedPool(tracer trace.Tracer) *positions.Pool {
	return positions.NewPool(positions.PoolParams{
		Queue:          e.queue,
		Pusher:         e.pusher,
		Metrics:        e.metrics,
		Workers:        1,
		RequestTimeout: requestTimeout,
		Logger:         discardLogger(),
		Tracer:         tracer,
	})
}

func TestPool_ActiveCountsBusyWorkers(t *testing.T) {
	e := newPoolEnv(t)
	e.queue.Enqueue(t.Context(), staleTrip(1))
	e.queue.Enqueue(t.Context(), staleTrip(2))
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, uuid.UUID, uuid.UUID) error {
			started <- struct{}{}
			<-release
			return nil
		}).Times(2)
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "success", gomock.Any()).Times(2)
	p := e.pool(4, requestTimeout)
	require.EqualValues(t, 0, p.Active())
	p.Start(t.Context())
	receive(t, started)
	receive(t, started)

	require.EqualValues(t, 2, p.Active(), "two of four workers are busy")

	close(release)
	drain(t, p)
	require.EqualValues(t, 0, p.Active())
}

func TestPool_ProcessingSpanIsLinkedToTheCheckSpanNotItsChild(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)).Tracer("test")
	e := newPoolEnv(t)
	checkCtx, check := tracer.Start(t.Context(), "positions.check")
	require.Equal(t, positions.ResultQueued, e.queue.Enqueue(checkCtx, staleTrip(1)))
	check.End() // the pass is over long before a worker gets to the task
	e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "success", gomock.Any())
	p := e.tracedPool(tracer)
	p.Start(t.Context())

	drain(t, p)

	var process sdktrace.ReadOnlySpan
	for _, s := range spans.Ended() {
		if s.Name() == "positions.process" {
			process = s
		}
	}
	require.NotNil(t, process)
	require.False(t, process.Parent().IsValid(), "the processing span has no parent")
	require.Len(t, process.Links(), 1)
	require.Equal(t, check.SpanContext().SpanID(), process.Links()[0].SpanContext.SpanID())
	require.Equal(t, check.SpanContext().TraceID(), process.Links()[0].SpanContext.TraceID())
	require.NotEqual(t, check.SpanContext().TraceID(), process.SpanContext().TraceID(), "it starts a trace of its own")
}

func TestPool_FailedPushMarksTheProcessingSpan(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)).Tracer("test")
	e := newPoolEnv(t)
	e.queue.Enqueue(t.Context(), staleTrip(1))
	e.pusher.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("boom"))
	e.metrics.EXPECT().PositionRequestFinished(gomock.Any(), "error", gomock.Any())
	p := e.tracedPool(tracer)
	p.Start(t.Context())

	drain(t, p)

	ended := spans.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, codes.Error, ended[0].Status().Code)
}
