package positions_test

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
)

// waitLimit only guards against a hung test; passing tests never wait for it.
const waitLimit = 5 * time.Second

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func staleTrip(n int) domain.StaleTrip {
	return domain.StaleTrip{
		TripID:   uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", n)),
		DriverID: uuid.MustParse(fmt.Sprintf("11111111-1111-1111-1111-%012d", n)),
	}
}

// fakeTicker is a Ticker the test drives by hand. The channel is unbuffered, so
// a tick is accepted only while the scanner is waiting for one.
type fakeTicker struct {
	c       chan time.Time
	stopped chan struct{}
}

func newFakeTicker() *fakeTicker {
	return &fakeTicker{c: make(chan time.Time), stopped: make(chan struct{})}
}

func (f *fakeTicker) C() <-chan time.Time { return f.c }
func (f *fakeTicker) Stop()               { close(f.stopped) }

// tick delivers one tick and returns once the scanner has taken it.
func (f *fakeTicker) tick(t *testing.T) {
	t.Helper()

	select {
	case f.c <- time.Time{}:
	case <-time.After(waitLimit):
		t.Fatal("scanner did not take the tick")
	}
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(waitLimit):
		t.Fatal("timed out waiting for an event")
		panic("unreachable")
	}
}
