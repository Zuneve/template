package positions

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// ScannerParams are the scanner's dependencies. Now is injectable so tests
// control the staleness threshold.
type ScannerParams struct {
	Finder     staleTripFinder
	Queue      *Queue
	Metrics    scannerMetrics
	Ticker     Ticker
	StaleAfter time.Duration
	Now        func() time.Time
	Logger     *slog.Logger
	// Tracer is optional; without it the scanner records no spans.
	Tracer trace.Tracer
}

// Scanner is one goroutine: on every tick it asks the database for trips that
// went silent and puts them in the queue. Ticks are handled one after another
// in that goroutine, so a slow pass can never overlap the next one, and a
// tick it missed is simply skipped.
type Scanner struct {
	p ScannerParams

	cancel context.CancelFunc
	done   chan struct{}
}

func NewScanner(p ScannerParams) *Scanner {
	if p.Now == nil {
		p.Now = time.Now
	}
	if p.Tracer == nil {
		p.Tracer = noop.NewTracerProvider().Tracer("")
	}
	return &Scanner{p: p, done: make(chan struct{})}
}

// Start launches the scanning goroutine. It runs until Stop is called or ctx
// is cancelled.
func (s *Scanner) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	s.p.Logger.Info("positions scanner started", slog.Duration("stale_after", s.p.StaleAfter))
	go s.run(ctx)
}

// Stop ends the scanner and waits for its goroutine, so that no new task can
// appear after it returns. It gives up waiting when ctx is done.
func (s *Scanner) Stop(ctx context.Context) error {
	s.cancel()
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Scanner) run(ctx context.Context) {
	defer close(s.done)
	defer s.p.Logger.Info("positions scanner stopped")
	defer s.p.Ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.p.Ticker.C():
			s.scan(ctx)
		}
	}
}

func (s *Scanner) scan(ctx context.Context) {
	ctx, span := s.p.Tracer.Start(ctx, "positions.check")
	defer span.End()

	staleBefore := s.p.Now().Add(-s.p.StaleAfter)
	trips, err := s.p.Finder.ListStaleTrips(ctx, staleBefore)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "select stale trips failed")
		if ctx.Err() == nil {
			s.p.Logger.ErrorContext(ctx, "select stale trips failed", slog.Any("error", err))
		}
		return
	}
	span.SetAttributes(attribute.Int("stale_trips", len(trips)))

	for _, trip := range trips {
		if ctx.Err() != nil {
			return
		}

		result := s.p.Queue.Enqueue(ctx, trip)
		s.p.Metrics.StaleCheck(ctx, string(result))
		if result == ResultDropped {
			s.p.Metrics.PositionRequestDropped(ctx)
			s.p.Logger.WarnContext(ctx, "position request dropped: queue is full",
				slog.String("trip_id", trip.TripID.String()),
			)
		}
	}
}
