package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/repository"
)

func (a *API) CreateTrip(w http.ResponseWriter, r *http.Request, params api.CreateTripParams) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, problemInvalidRequest("request body could not be read: "+err.Error()))
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		writeProblem(w, r, http.StatusBadRequest, problemInvalidRequest("request body is not valid JSON: "+err.Error()))
		return
	}
	if detail := missingTripDataField(fields); detail != "" {
		writeProblem(w, r, http.StatusBadRequest, problemInvalidRequest(detail))
		return
	}

	var body api.TripData
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeProblem(w, r, http.StatusBadRequest, problemInvalidRequest("request body is not valid JSON: "+err.Error()))
		return
	}

	if detail, ok := validateTripData(body); !ok {
		writeProblem(w, r, http.StatusBadRequest, problemInvalidRequest(detail))
		return
	}

	trip := repository.Trip{
		ID:         uuid.New(),
		UserID:     body.UserId,
		DriverID:   body.DriverId,
		StartPoint: repository.Point(body.StartPoint),
		EndPoint:   repository.Point(body.EndPoint),
		Price:      body.Price,
		Status:     repository.TripStatusActive,
		StartedAt:  time.Now().UTC(),
	}

	ctx, cancel := a.withQueryTimeout(r.Context())
	defer cancel()

	if params.IdempotencyKey == nil {
		if err := a.createTrip(ctx, trip); err != nil {
			writeDomainError(w, r, err)
			return
		}
		w.Header().Set("Location", "/api/v1/trips/"+trip.ID.String())
		writeJSON(w, http.StatusCreated, toAPITrip(trip))
		return
	}

	result, status, err := a.createTripIdempotent(ctx, *params.IdempotencyKey, body, trip)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if status == http.StatusCreated {
		w.Header().Set("Location", "/api/v1/trips/"+result.ID.String())
	}
	writeJSON(w, status, toAPITrip(result))
}

// Generated Go structs cannot distinguish a missing required number from a
// supplied zero. Check field presence before decoding into the generated type.
func missingTripDataField(fields map[string]json.RawMessage) string {
	for _, name := range []string{"user_id", "driver_id", "start_point", "end_point", "price"} {
		value, ok := fields[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return name + " is required"
		}
	}
	for _, name := range []string{"start_point", "end_point"} {
		var coordinates map[string]json.RawMessage
		if err := json.Unmarshal(fields[name], &coordinates); err != nil {
			return name + " must be an object"
		}
		for _, axis := range []string{"latitude", "longitude"} {
			value, ok := coordinates[axis]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return name + "." + axis + " is required"
			}
		}
	}
	return ""
}

func (a *API) createTrip(ctx context.Context, trip repository.Trip) error {
	return a.txManager.Do(ctx, func(ctx context.Context) error {
		if err := a.trips.Create(ctx, trip); err != nil {
			return err
		}
		return a.history.Insert(ctx, repository.StatusHistoryEntry{
			TripID:   trip.ID,
			ToStatus: repository.TripStatusActive,
		})
	})
}

// createTripIdempotent implements the Idempotency-Key bonus task: a repeat
// request with the same key and the same body replays the trip that was
// created for it (200); the same key with a different body is a conflict.
// The idempotency record is inserted in the same transaction as the trip and
// its history row, so a request can never observe a trip whose key isn't
// claimed yet. A concurrent request racing on the same brand-new key loses
// the key insert (unique_violation) and is resolved the same way as a replay
// of an already-known key.
func (a *API) createTripIdempotent(ctx context.Context, key uuid.UUID, body api.TripData, trip repository.Trip) (repository.Trip, int, error) {
	if err := a.idempotency.DeleteExpired(ctx); err != nil {
		return repository.Trip{}, 0, err
	}
	canonical, err := json.Marshal(body)
	if err != nil {
		return repository.Trip{}, 0, fmt.Errorf("marshal trip data for idempotency hash: %w", err)
	}
	hash := requestHash(canonical)

	existing, err := a.idempotency.Get(ctx, key)
	if err == nil {
		return a.resolveIdempotentReplay(ctx, existing, hash)
	}
	if !errors.Is(err, repository.ErrIdempotencyKeyNotFound) {
		return repository.Trip{}, 0, err
	}

	err = a.txManager.Do(ctx, func(ctx context.Context) error {
		// Claim the key before touching trips: if two concurrent requests
		// share this same brand-new key, the loser must fail here (unique
		// key conflict) rather than in trips.Create, where a shared
		// driver_id would otherwise surface as a misleading driver_busy.
		// The FK on trip_id is DEFERRABLE, so it's fine that the trip row
		// doesn't exist yet — it's checked at COMMIT, not at this INSERT.
		if err := a.idempotency.Insert(ctx, repository.IdempotencyRecord{
			Key:         key,
			RequestHash: hash,
			TripID:      trip.ID,
		}); err != nil {
			return err
		}
		if err := a.trips.Create(ctx, trip); err != nil {
			return err
		}
		return a.history.Insert(ctx, repository.StatusHistoryEntry{
			TripID:   trip.ID,
			ToStatus: repository.TripStatusActive,
		})
	})
	switch {
	case err == nil:
		return trip, http.StatusCreated, nil
	case errors.Is(err, repository.ErrIdempotencyKeyConflict):
		existing, getErr := a.idempotency.Get(ctx, key)
		if getErr != nil {
			return repository.Trip{}, 0, getErr
		}
		return a.resolveIdempotentReplay(ctx, existing, hash)
	default:
		return repository.Trip{}, 0, err
	}
}

func (a *API) resolveIdempotentReplay(ctx context.Context, existing repository.IdempotencyRecord, hash string) (repository.Trip, int, error) {
	if existing.RequestHash != hash {
		return repository.Trip{}, 0, errIdempotencyConflict
	}
	trip, err := a.trips.GetByID(ctx, existing.TripID)
	if err != nil {
		return repository.Trip{}, 0, err
	}
	return trip, http.StatusOK, nil
}

func requestHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (a *API) GetTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	ctx, cancel := a.withQueryTimeout(r.Context())
	defer cancel()

	trip, err := a.trips.GetByID(ctx, tripId)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPITrip(trip))
}

func (a *API) FinishTrip(w http.ResponseWriter, r *http.Request, tripId api.TripId) {
	ctx, cancel := a.withQueryTimeout(r.Context())
	defer cancel()

	var trip repository.Trip
	err := a.txManager.Do(ctx, func(ctx context.Context) error {
		var err error
		trip, err = a.trips.Finish(ctx, tripId)
		if err != nil {
			return err
		}
		fromStatus := repository.TripStatusActive
		return a.history.Insert(ctx, repository.StatusHistoryEntry{
			TripID:     trip.ID,
			FromStatus: &fromStatus,
			ToStatus:   repository.TripStatusCompleted,
		})
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAPITrip(trip))
}

// validateTripData checks the business rules from README section 5.2 that
// aren't already enforced by JSON decoding (additionalProperties: false is
// handled by DisallowUnknownFields, malformed UUIDs by uuid.UUID's
// UnmarshalText).
func validateTripData(body api.TripData) (detail string, ok bool) {
	if body.UserId == uuid.Nil {
		return "user_id is required", false
	}
	if body.DriverId == uuid.Nil {
		return "driver_id is required", false
	}
	if !validCoordinates(body.StartPoint) {
		return "start_point is out of range", false
	}
	if !validCoordinates(body.EndPoint) {
		return "end_point is out of range", false
	}
	if body.Price < 0 {
		return "price must be >= 0", false
	}
	return "", true
}

func validCoordinates(c api.Coordinates) bool {
	return c.Latitude >= -90 && c.Latitude <= 90 &&
		c.Longitude >= -180 && c.Longitude <= 180
}

func toAPITrip(t repository.Trip) api.Trip {
	return api.Trip{
		Id:         t.ID,
		UserId:     t.UserID,
		DriverId:   t.DriverID,
		StartPoint: api.Coordinates(t.StartPoint),
		EndPoint:   api.Coordinates(t.EndPoint),
		Price:      t.Price,
		Status:     api.TripStatus(t.Status),
		StartedAt:  t.StartedAt,
		FinishedAt: t.FinishedAt,
	}
}
