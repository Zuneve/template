package positions

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	resultSuccess = "success"
	resultError   = "error"
)

// PoolParams are the worker pool's dependencies.
type PoolParams struct {
	Queue          *Queue
	Pusher         pusher
	Metrics        poolMetrics
	Workers        int
	RequestTimeout time.Duration
	Logger         *slog.Logger
	// Tracer is optional; without it the pool records no spans.
	Tracer trace.Tracer
}

// Pool is a fixed set of workers that live from Start until they are shut
// down. A task never gets a goroutine of its own.
type Pool struct {
	p PoolParams

	cancel context.CancelFunc
	wg     sync.WaitGroup
	active atomic.Int64
}

func NewPool(p PoolParams) *Pool {
	if p.Tracer == nil {
		p.Tracer = noop.NewTracerProvider().Tracer("")
	}
	return &Pool{p: p}
}

// Active is the number of workers busy with a task right now.
func (p *Pool) Active() int64 {
	return p.active.Load()
}

// Start launches the workers. ctx is the lifetime of the pool: cancelling it
// aborts the Push calls in flight and makes the workers leave at once. For a
// graceful stop use Shutdown instead.
func (p *Pool) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)

	p.p.Logger.Info("positions workers started",
		slog.Int("workers", p.p.Workers),
		slog.Int("queue_size", cap(p.p.Queue.tasks)),
	)
	p.wg.Add(p.p.Workers)
	for range p.p.Workers {
		go p.work(ctx)
	}
}

// Shutdown closes the queue and lets the workers finish the tasks already in
// it. If ctx runs out first, the Push calls in flight are cancelled, the rest
// of the queue is abandoned and ctx's error is returned. Either way every
// worker has exited when Shutdown returns.
func (p *Pool) Shutdown(ctx context.Context) error {
	p.p.Queue.Close()

	drained := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(drained)
	}()

	select {
	case <-drained:
		p.cancel()
		p.p.Logger.Info("positions workers stopped")
		return nil
	case <-ctx.Done():
		abandoned := p.p.Queue.Len()
		p.cancel()
		<-drained
		p.p.Logger.Warn("positions workers stopped before the queue was drained",
			slog.Int("abandoned_tasks", abandoned),
		)
		return ctx.Err()
	}
}

func (p *Pool) work(ctx context.Context) {
	defer p.wg.Done()

	for {
		// Check first: when both the context and a task are ready, select picks
		// at random, and a cancelled pool must not start another task.
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case t, ok := <-p.p.Queue.tasks:
			if !ok {
				return
			}
			p.handle(ctx, t)
		}
	}
}

func (p *Pool) handle(ctx context.Context, t task) {
	if !p.p.Queue.claim(t) {
		// A position arrived after the trip was queued: the device is alive.
		return
	}
	defer p.p.Queue.done(t)

	p.active.Add(1)
	defer p.active.Add(-1)

	// A new root linked to the check span: this runs later and on another
	// goroutine, so the check is where the task came from, not its parent.
	opts := []trace.SpanStartOption{
		trace.WithNewRoot(),
		trace.WithAttributes(attribute.String("trip_id", t.tripID.String())),
	}
	if t.check.IsValid() {
		opts = append(opts, trace.WithLinks(trace.Link{SpanContext: t.check}))
	}
	ctx, span := p.p.Tracer.Start(ctx, "positions.process", opts...)
	defer span.End()

	callCtx, cancel := context.WithTimeout(ctx, p.p.RequestTimeout)
	defer cancel()

	start := time.Now()
	err := p.p.Pusher.RequestPosition(callCtx, t.driverID, t.tripID)
	elapsed := time.Since(start)

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "request position failed")
		p.p.Metrics.PositionRequestFinished(ctx, resultError, elapsed)
		p.p.Logger.WarnContext(ctx, "request position failed",
			slog.String("trip_id", t.tripID.String()),
			slog.String("driver_id", t.driverID.String()),
			slog.Any("error", err),
		)
		return
	}
	p.p.Metrics.PositionRequestFinished(ctx, resultSuccess, elapsed)
}
