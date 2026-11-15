package positions

import (
	"context"
	"sync"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	domain "github.com/Zuneve/template/internal/domain/trip"
)

// Result is what Enqueue did with a trip; the values are the `result` label of
// tripgo.stale_checks.
type Result string

const (
	ResultQueued    Result = "queued"
	ResultDuplicate Result = "duplicate"
	ResultDropped   Result = "dropped"
)

type task struct {
	tripID   uuid.UUID
	driverID uuid.UUID
	// seq tells apart two tasks of the same trip: after Cancel and a new
	// Enqueue the old task is still in the channel, and only the new one may
	// reach the Push Service.
	seq uint64
	// check is the span of the scan pass that queued the task. The worker links
	// its own span to it instead of making it a parent: processing happens
	// later, on another goroutine, and outlives the pass.
	check trace.SpanContext
}

// Queue is a bounded buffered channel of tasks plus the "currently queued" set
// that the channel alone cannot give: one trip is in the queue at most once,
// and a trip whose position has just arrived can be taken back. A trip stays in
// the set from Enqueue until a worker has finished it, so the scanner's next
// tick cannot add it again while it is waiting or being processed.
//
// The scanner, the workers and the position handlers all use it at once; mu
// guards the set and the closed flag.
type Queue struct {
	tasks chan task

	mu     sync.Mutex
	queued map[uuid.UUID]uint64
	next   uint64
	closed bool
}

func NewQueue(size int) *Queue {
	return &Queue{
		tasks:  make(chan task, size),
		queued: make(map[uuid.UUID]uint64),
	}
}

// Enqueue never blocks. A trip that is already queued is a duplicate; a full or
// closed queue drops the task. The span in ctx, if any, is the check span the
// task is later linked to.
func (q *Queue) Enqueue(ctx context.Context, trip domain.StaleTrip) Result {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return ResultDropped
	}
	if _, ok := q.queued[trip.TripID]; ok {
		return ResultDuplicate
	}

	q.next++
	t := task{tripID: trip.TripID, driverID: trip.DriverID, seq: q.next, check: trace.SpanContextFromContext(ctx)}
	select {
	case q.tasks <- t:
		q.queued[trip.TripID] = t.seq
		return ResultQueued
	default:
		return ResultDropped
	}
}

// Cancel takes the trip out of the queued set. A task already in the channel
// stays there, but the worker that picks it up sees the trip is gone and does
// not call the Push Service. Cancelling a trip that is not queued is a no-op.
func (q *Queue) Cancel(tripID uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()

	delete(q.queued, tripID)
}

// Len is the number of tasks waiting in the channel.
func (q *Queue) Len() int {
	return len(q.tasks)
}

// Close stops accepting tasks and lets workers drain what is left. Safe to
// call more than once.
func (q *Queue) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.closed {
		return
	}
	q.closed = true
	close(q.tasks)
}

// claim reports whether the task is still wanted: its trip is queued and it is
// the same enqueue, not an older one that was cancelled and queued again.
func (q *Queue) claim(t task) bool {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.queued[t.tripID] == t.seq
}

// done removes the trip once its task is processed. A newer enqueue of the same
// trip is left alone.
func (q *Queue) done(t task) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.queued[t.tripID] == t.seq {
		delete(q.queued, t.tripID)
	}
}
