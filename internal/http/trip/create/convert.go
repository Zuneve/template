package create

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"

	domain "github.com/Zuneve/template/internal/domain/trip"
	api "github.com/Zuneve/template/internal/generated"
	createusecase "github.com/Zuneve/template/internal/usecase/trip/create"
)

const maxBodyBytes = 1 << 20

// decodeRequest reads and checks the request body. A non-empty detail is the
// reason to answer 400 invalid_request.
func decodeRequest(w http.ResponseWriter, r *http.Request) (body api.TripData, detail string) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return body, "request body could not be read: " + err.Error()
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return body, "request body is not valid JSON: " + err.Error()
	}
	if detail := missingField(fields); detail != "" {
		return body, detail
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return body, "request body is not valid JSON: " + err.Error()
	}
	return body, ""
}

// missingField checks field presence before decoding into the generated type:
// its Go structs cannot tell a missing required number from a supplied zero.
func missingField(fields map[string]json.RawMessage) string {
	for _, name := range []string{"user_id", "driver_id", "start_point", "end_point", "price"} {
		if isMissing(fields[name]) {
			return name + " is required"
		}
	}
	for _, name := range []string{"start_point", "end_point"} {
		var coordinates map[string]json.RawMessage
		if err := json.Unmarshal(fields[name], &coordinates); err != nil {
			return name + " must be an object"
		}
		for _, axis := range []string{"latitude", "longitude"} {
			if isMissing(coordinates[axis]) {
				return name + "." + axis + " is required"
			}
		}
	}
	return ""
}

func isMissing(value json.RawMessage) bool {
	return len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func toInput(body api.TripData, idempotencyKey *uuid.UUID) createusecase.Input {
	return createusecase.Input{
		UserID:         body.UserId,
		DriverID:       body.DriverId,
		StartPoint:     domain.Point(body.StartPoint),
		EndPoint:       domain.Point(body.EndPoint),
		Price:          body.Price,
		IdempotencyKey: idempotencyKey,
	}
}
