package positions_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/worker/positions"
)

var scanNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

const staleAfter = time.Minute

type scannerEnv struct {
	finder  *MockstaleTripFinder
	metrics *MockscannerMetrics
	queue   *positions.Queue
	ticker  *fakeTicker
	scanner *positions.Scanner
	// checks gets every result passed to StaleCheck, in order.
	checks chan string
}

func newScannerEnv(t *testing.T, queueSize int) *scannerEnv {
	t.Helper()

	ctrl := gomock.NewController(t)
	e := &scannerEnv{
		finder:  NewMockstaleTripFinder(ctrl),
		metrics: NewMockscannerMetrics(ctrl),
		queue:   positions.NewQueue(queueSize),
		ticker:  newFakeTicker(),
		checks:  make(chan string, 100),
	}
	e.metrics.EXPECT().StaleCheck(gomock.Any(), gomock.Any()).
		Do(func(_ context.Context, result string) { e.checks <- result }).AnyTimes()
	e.scanner = positions.NewScanner(positions.ScannerParams{
		Finder:     e.finder,
		Queue:      e.queue,
		Metrics:    e.metrics,
		Ticker:     e.ticker,
		StaleAfter: staleAfter,
		Now:        func() time.Time { return scanNow },
		Logger:     discardLogger(),
	})
	return e
}

func (e *scannerEnv) start(t *testing.T) {
	t.Helper()

	e.scanner.Start(t.Context())
	t.Cleanup(func() { require.NoError(t, e.scanner.Stop(context.Background())) })
}

func TestScanner_QueuesStaleTripsOnTick(t *testing.T) {
	e := newScannerEnv(t, 10)
	e.finder.EXPECT().ListStaleTrips(gomock.Any(), scanNow.Add(-staleAfter)).
		Return([]domain.StaleTrip{staleTrip(1), staleTrip(2)}, nil)
	e.start(t)

	e.ticker.tick(t)

	require.Equal(t, "queued", receive(t, e.checks))
	require.Equal(t, "queued", receive(t, e.checks))
	require.Equal(t, 2, e.queue.Len())
}

func TestScanner_TripAlreadyInQueueIsNotQueuedAgain(t *testing.T) {
	e := newScannerEnv(t, 10)
	e.finder.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any()).
		Return([]domain.StaleTrip{staleTrip(1)}, nil).Times(2)
	e.start(t)

	e.ticker.tick(t)
	require.Equal(t, "queued", receive(t, e.checks))
	e.ticker.tick(t)

	require.Equal(t, "duplicate", receive(t, e.checks))
	require.Equal(t, 1, e.queue.Len())
}

func TestScanner_FullQueueDropsInsteadOfBlocking(t *testing.T) {
	e := newScannerEnv(t, 1)
	e.finder.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any()).
		Return([]domain.StaleTrip{staleTrip(1), staleTrip(2), staleTrip(3)}, nil)
	var dropped atomic.Int32
	e.metrics.EXPECT().PositionRequestDropped(gomock.Any()).Do(func(context.Context) { dropped.Add(1) }).Times(2)
	e.start(t)

	// No worker takes anything out, so a blocking Enqueue would hang here.
	e.ticker.tick(t)

	require.Equal(t, "queued", receive(t, e.checks))
	require.Equal(t, "dropped", receive(t, e.checks))
	require.Equal(t, "dropped", receive(t, e.checks))
	require.NoError(t, e.scanner.Stop(context.Background()))
	require.EqualValues(t, 2, dropped.Load())
}

func TestScanner_SlowPassIsNotOverlapped(t *testing.T) {
	e := newScannerEnv(t, 10)
	var running, maxRunning atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	e.finder.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, time.Time) ([]domain.StaleTrip, error) {
			n := running.Add(1)
			defer running.Add(-1)
			for {
				prev := maxRunning.Load()
				if n <= prev || maxRunning.CompareAndSwap(prev, n) {
					break
				}
			}
			entered <- struct{}{}
			<-release
			return nil, nil
		}).Times(2)
	e.start(t)

	e.ticker.tick(t)
	receive(t, entered)

	// The first pass is still running, so nobody is receiving ticks.
	select {
	case e.ticker.c <- time.Time{}:
		t.Fatal("a second tick was accepted while the first pass was still running")
	default:
	}

	release <- struct{}{}
	e.ticker.tick(t)
	receive(t, entered)
	release <- struct{}{}

	require.NoError(t, e.scanner.Stop(context.Background()))
	require.EqualValues(t, 1, maxRunning.Load())
}

func TestScanner_FinderErrorDoesNotStopScanning(t *testing.T) {
	e := newScannerEnv(t, 10)
	gomock.InOrder(
		e.finder.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any()).Return(nil, errors.New("db is down")),
		e.finder.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any()).Return([]domain.StaleTrip{staleTrip(1)}, nil),
	)
	e.start(t)

	e.ticker.tick(t)
	e.ticker.tick(t)

	require.Equal(t, "queued", receive(t, e.checks))
}

func TestScanner_StopEndsGoroutineAndTicker(t *testing.T) {
	e := newScannerEnv(t, 1)
	e.scanner.Start(t.Context())

	require.NoError(t, e.scanner.Stop(context.Background()))

	receive(t, e.ticker.stopped)
}

func TestScanner_ContextCancellationEndsGoroutine(t *testing.T) {
	e := newScannerEnv(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	e.scanner.Start(ctx)

	cancel()

	receive(t, e.ticker.stopped)
	require.NoError(t, e.scanner.Stop(context.Background()))
}

func TestScanner_StopGivesUpWhenContextIsDone(t *testing.T) {
	e := newScannerEnv(t, 1)
	entered := make(chan struct{})
	release := make(chan struct{})
	e.finder.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, time.Time) ([]domain.StaleTrip, error) {
			close(entered)
			<-release
			return nil, nil
		})
	e.scanner.Start(t.Context())
	e.ticker.tick(t)
	receive(t, entered)

	stopCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err := e.scanner.Stop(stopCtx)

	require.ErrorIs(t, err, context.Canceled)
	close(release)
	receive(t, e.ticker.stopped)
}

func TestScanner_QueuedTaskCarriesTheCheckSpan(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)).Tracer("test")
	e := newScannerEnv(t, 10)
	e.scanner = positions.NewScanner(positions.ScannerParams{
		Finder: e.finder, Queue: e.queue, Metrics: e.metrics, Ticker: e.ticker,
		StaleAfter: staleAfter, Now: func() time.Time { return scanNow },
		Logger: discardLogger(), Tracer: tracer,
	})
	e.finder.EXPECT().ListStaleTrips(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ time.Time) ([]domain.StaleTrip, error) {
			require.True(t, trace.SpanContextFromContext(ctx).IsValid(), "the query runs inside the check span")
			return []domain.StaleTrip{staleTrip(1)}, nil
		})
	e.start(t)

	e.ticker.tick(t)
	require.Equal(t, "queued", receive(t, e.checks))
	require.NoError(t, e.scanner.Stop(context.Background()))

	ended := spans.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, "positions.check", ended[0].Name())
	require.Contains(t, ended[0].Attributes(), attribute.Int("stale_trips", 1))
}
