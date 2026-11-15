// Package domainerror is the single place that turns an error coming out of a
// usecase into an HTTP status and an RFC 9457 problem body.
package domainerror

import (
	"errors"
	"net/http"

	domain "github.com/Zuneve/template/internal/domain/trip"
	api "github.com/Zuneve/template/internal/generated"
)

const problemTypeBase = "https://tripgo.example/problems/"

// Problem maps err to a status and a problem body. Errors it does not know
// are internal: their text is never put into the body.
func Problem(err error) (status int, body api.Problem) {
	var validationErr *domain.ValidationError

	switch {
	case errors.As(err, &validationErr):
		return InvalidRequest(validationErr.Error())
	case errors.Is(err, domain.ErrTripNotFound):
		return problem(http.StatusNotFound, "trip-not-found", "Trip not found", "trip_not_found",
			"Trip was not found")
	case errors.Is(err, domain.ErrTripCompleted):
		return problem(http.StatusConflict, "trip-completed", "Trip completed", "trip_completed",
			"Operation is not allowed for a completed trip")
	case errors.Is(err, domain.ErrDriverBusy):
		return problem(http.StatusConflict, "driver-busy", "Driver busy", "driver_busy",
			"Driver already has an active trip")
	case errors.Is(err, domain.ErrIdempotencyKeyReused):
		return problem(http.StatusConflict, "idempotency-conflict", "Idempotency conflict", "idempotency_conflict",
			"Idempotency-Key was already used with a different request body")
	default:
		return InternalError()
	}
}

// InvalidRequest is the 400 problem for a request the client got wrong.
func InvalidRequest(detail string) (status int, body api.Problem) {
	return problem(http.StatusBadRequest, "invalid-request", "Invalid request", "invalid_request", detail)
}

func InternalError() (status int, body api.Problem) {
	return problem(http.StatusInternalServerError, "internal-error", "Internal Server Error", "internal_error",
		"Internal server error")
}

func problem(status int32, slug, title, code, detail string) (int, api.Problem) {
	return int(status), api.Problem{
		Type:   problemTypeBase + slug,
		Title:  title,
		Code:   code,
		Status: status,
		Detail: &detail,
	}
}
