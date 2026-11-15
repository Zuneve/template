//go:generate go tool mockgen -source ${GOFILE} -destination mocks_test.go -package ${GOPACKAGE}_test
package positions

import (
	"context"
	"time"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
)

type (
	staleTripFinder interface {
		ListStaleTrips(ctx context.Context, staleBefore time.Time) ([]domain.StaleTrip, error)
	}

	pusher interface {
		RequestPosition(ctx context.Context, driverID, tripID uuid.UUID) error
	}

	scannerMetrics interface {
		StaleCheck(ctx context.Context, result string)
		PositionRequestDropped(ctx context.Context)
	}

	poolMetrics interface {
		PositionRequestFinished(ctx context.Context, result string, duration time.Duration)
	}
)

// Ticker is the part of time.Ticker the scanner needs. Production code uses
// NewTicker; tests feed ticks by hand, so they never wait for real time.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type realTicker struct{ *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.Ticker.C }

// NewTicker is a Ticker over time.NewTicker.
func NewTicker(period time.Duration) Ticker {
	return realTicker{time.NewTicker(period)}
}
