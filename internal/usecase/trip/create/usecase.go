package create

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/model"
)

type Input struct {
	UserID         uuid.UUID
	DriverID       uuid.UUID
	StartPoint     domain.Point
	EndPoint       domain.Point
	Price          int64
	IdempotencyKey *uuid.UUID
}

type Output struct {
	Trip domain.Trip
	// Created is false when the trip is a replay of an earlier request with
	// the same Idempotency-Key.
	Created bool
}

type Usecase struct {
	trips       tripRepository
	history     historyRepository
	idempotency idempotencyRepository
	tx          txManager
	metrics     tripMetrics
	logger      *slog.Logger
}

func New(
	trips tripRepository,
	history historyRepository,
	idempotency idempotencyRepository,
	tx txManager,
	metrics tripMetrics,
	logger *slog.Logger,
) *Usecase {
	return &Usecase{
		trips:       trips,
		history:     history,
		idempotency: idempotency,
		tx:          tx,
		metrics:     metrics,
		logger:      logger,
	}
}

func (u *Usecase) Execute(ctx context.Context, in Input) (Output, error) {
	trip, err := domain.New(domain.NewParams{
		ID:         uuid.New(),
		UserID:     in.UserID,
		DriverID:   in.DriverID,
		StartPoint: in.StartPoint,
		EndPoint:   in.EndPoint,
		Price:      in.Price,
		StartedAt:  time.Now().UTC(),
	})
	if err != nil {
		return Output{}, err
	}

	if in.IdempotencyKey == nil {
		if err := u.tx.Do(ctx, func(ctx context.Context) error {
			return u.save(ctx, trip)
		}); err != nil {
			return Output{}, err
		}
		u.created(ctx, trip)
		return Output{Trip: trip, Created: true}, nil
	}
	return u.executeIdempotent(ctx, *in.IdempotencyKey, requestHash(in), trip)
}

// executeIdempotent implements Idempotency-Key: a repeat request with the same
// key and the same body replays the trip created for it; the same key with a
// different body is a conflict. The key is claimed in the same transaction as
// the trip and its history row, so nobody can see a trip whose key is not
// claimed yet. A concurrent request racing on a brand-new key loses the key
// insert and is resolved the same way as a replay of an already known key.
func (u *Usecase) executeIdempotent(ctx context.Context, key uuid.UUID, hash string, trip domain.Trip) (Output, error) {
	if err := u.idempotency.DeleteExpired(ctx); err != nil {
		return Output{}, fmt.Errorf("delete expired idempotency keys: %w", err)
	}

	existing, err := u.idempotency.Get(ctx, key)
	if err == nil {
		return u.replay(ctx, existing, hash)
	}
	if !errors.Is(err, domain.ErrIdempotencyKeyNotFound) {
		return Output{}, fmt.Errorf("get idempotency key: %w", err)
	}

	err = u.tx.Do(ctx, func(ctx context.Context) error {
		// The key goes first: if two concurrent requests share a brand-new
		// key, the loser must fail on the key (unique violation), not on
		// trips, where a shared driver_id would surface as a misleading
		// ErrDriverBusy. The FK from idempotency_keys to trips is DEFERRABLE,
		// so the trip row may be inserted after the key within the transaction.
		if err := u.idempotency.Insert(ctx, model.IdempotencyRecord{
			Key:         key,
			RequestHash: hash,
			TripID:      trip.ID,
		}); err != nil {
			return err
		}
		return u.save(ctx, trip)
	})
	switch {
	case err == nil:
		u.created(ctx, trip)
		return Output{Trip: trip, Created: true}, nil
	case errors.Is(err, domain.ErrIdempotencyKeyConflict):
		existing, getErr := u.idempotency.Get(ctx, key)
		if getErr != nil {
			return Output{}, fmt.Errorf("get idempotency key after conflict: %w", getErr)
		}
		return u.replay(ctx, existing, hash)
	default:
		return Output{}, err
	}
}

func (u *Usecase) replay(ctx context.Context, existing model.IdempotencyRecord, hash string) (Output, error) {
	if existing.RequestHash != hash {
		return Output{}, domain.ErrIdempotencyKeyReused
	}
	trip, err := u.trips.GetByID(ctx, existing.TripID)
	if err != nil {
		return Output{}, fmt.Errorf("get replayed trip: %w", err)
	}
	return Output{Trip: trip, Created: false}, nil
}

// save inserts the trip together with its first status history row. It must
// run inside a transaction.
func (u *Usecase) save(ctx context.Context, trip domain.Trip) error {
	if err := u.trips.Create(ctx, trip); err != nil {
		return err
	}
	return u.history.AddStatusHistory(ctx, model.StatusHistoryEntry{
		TripID:   trip.ID,
		ToStatus: domain.StatusActive,
	})
}

// created reports a trip that was really created, not replayed.
func (u *Usecase) created(ctx context.Context, trip domain.Trip) {
	u.metrics.TripCreated(ctx)
	u.logger.InfoContext(ctx, "trip created", slog.String("trip_id", trip.ID.String()))
}

// requestHash identifies the request body; it does not include the key itself.
func requestHash(in Input) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\n%s\n%g,%g\n%g,%g\n%d",
		in.UserID, in.DriverID,
		in.StartPoint.Latitude, in.StartPoint.Longitude,
		in.EndPoint.Latitude, in.EndPoint.Longitude,
		in.Price,
	)
	return hex.EncodeToString(h.Sum(nil))
}
