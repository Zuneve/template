package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/repository"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeProblem writes an RFC 9457 application/problem+json response. status
// and instance are filled in here so callers only supply the parts that are
// fixed per error kind (type, title, code) plus a request-specific detail.
func writeProblem(w http.ResponseWriter, r *http.Request, status int, p api.Problem) {
	instance := r.URL.Path
	p.Instance = &instance
	p.Status = int32(status)

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

func detailPtr(detail string) *string {
	return &detail
}

func problemInvalidRequest(detail string) api.Problem {
	return api.Problem{
		Type:   "https://tripgo.example/problems/invalid-request",
		Title:  "Invalid request",
		Code:   "invalid_request",
		Detail: detailPtr(detail),
	}
}

func problemTripNotFound() api.Problem {
	return api.Problem{
		Type:   "https://tripgo.example/problems/trip-not-found",
		Title:  "Trip not found",
		Code:   "trip_not_found",
		Detail: detailPtr("Trip was not found"),
	}
}

func problemTripCompleted() api.Problem {
	return api.Problem{
		Type:   "https://tripgo.example/problems/trip-completed",
		Title:  "Trip completed",
		Code:   "trip_completed",
		Detail: detailPtr("Operation is not allowed for a completed trip"),
	}
}

func problemDriverBusy() api.Problem {
	return api.Problem{
		Type:   "https://tripgo.example/problems/driver-busy",
		Title:  "Driver busy",
		Code:   "driver_busy",
		Detail: detailPtr("Driver already has an active trip"),
	}
}

func problemIdempotencyConflict() api.Problem {
	return api.Problem{
		Type:   "https://tripgo.example/problems/idempotency-conflict",
		Title:  "Idempotency conflict",
		Code:   "idempotency_conflict",
		Detail: detailPtr("Idempotency-Key was already used with a different request body"),
	}
}

func problemInternalError() api.Problem {
	return api.Problem{
		Type:   "https://tripgo.example/problems/internal-error",
		Title:  "Internal Server Error",
		Code:   "internal_error",
		Detail: detailPtr("Internal server error"),
	}
}

// errIdempotencyConflict signals that Idempotency-Key was reused with a
// request body that hashes differently from the one it was first claimed
// with; see resolveIdempotentReplay in trips.go.
var errIdempotencyConflict = errors.New("idempotency key used with a different request body")

// writeDomainError maps a repository error to the HTTP status and problem
// body required by the contract. Anything not recognized is an internal
// error: its real text is logged, never sent to the client.
func writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, repository.ErrTripNotFound):
		writeProblem(w, r, http.StatusNotFound, problemTripNotFound())
	case errors.Is(err, repository.ErrTripCompleted):
		writeProblem(w, r, http.StatusConflict, problemTripCompleted())
	case errors.Is(err, repository.ErrDriverBusy):
		writeProblem(w, r, http.StatusConflict, problemDriverBusy())
	case errors.Is(err, errIdempotencyConflict):
		writeProblem(w, r, http.StatusConflict, problemIdempotencyConflict())
	default:
		log.Printf("internal error: %v", err)
		writeProblem(w, r, http.StatusInternalServerError, problemInternalError())
	}
}
