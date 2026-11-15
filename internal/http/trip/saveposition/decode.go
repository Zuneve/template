package saveposition

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	api "github.com/Zuneve/template/internal/generated"
)

const maxBodyBytes = 1 << 20

// decodeRequest reads and checks the request body. A non-empty detail is the
// reason to answer 400 invalid_request. A recorded_at that does not parse as
// an RFC 3339 time fails here, inside the JSON decoder.
func decodeRequest(w http.ResponseWriter, r *http.Request) (body api.PositionData, detail string) {
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
// its Go structs cannot tell a missing required number from a supplied zero,
// and a zero latitude or longitude is a legitimate coordinate.
func missingField(fields map[string]json.RawMessage) string {
	for _, name := range []string{"coordinates", "recorded_at"} {
		if isMissing(fields[name]) {
			return name + " is required"
		}
	}

	var coordinates map[string]json.RawMessage
	if err := json.Unmarshal(fields["coordinates"], &coordinates); err != nil {
		return "coordinates must be an object"
	}
	for _, axis := range []string{"latitude", "longitude"} {
		if isMissing(coordinates[axis]) {
			return "coordinates." + axis + " is required"
		}
	}
	return ""
}

func isMissing(value json.RawMessage) bool {
	return len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}
