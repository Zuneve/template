package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	api "github.com/Zuneve/template/internal/generated"
)

func TestCreateTripRejectsIncompleteBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"missing start point", `{"user_id":"11111111-1111-1111-1111-111111111111","driver_id":"22222222-2222-2222-2222-222222222222","end_point":{"latitude":0,"longitude":0},"price":0}`},
		{"missing latitude", `{"user_id":"11111111-1111-1111-1111-111111111111","driver_id":"22222222-2222-2222-2222-222222222222","start_point":{"longitude":0},"end_point":{"latitude":0,"longitude":0},"price":0}`},
		{"missing price", `{"user_id":"11111111-1111-1111-1111-111111111111","driver_id":"22222222-2222-2222-2222-222222222222","start_point":{"latitude":0,"longitude":0},"end_point":{"latitude":0,"longitude":0}}`},
		{"trailing JSON", `{"user_id":"11111111-1111-1111-1111-111111111111","driver_id":"22222222-2222-2222-2222-222222222222","start_point":{"latitude":0,"longitude":0},"end_point":{"latitude":0,"longitude":0},"price":0} {}`},
		{"unknown nested field", `{"user_id":"11111111-1111-1111-1111-111111111111","driver_id":"22222222-2222-2222-2222-222222222222","start_point":{"latitude":0,"longitude":0,"extra":1},"end_point":{"latitude":0,"longitude":0},"price":0}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := NewAPI(nil, nil, nil, nil, nil, time.Second)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/trips", strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			handler.CreateTrip(rec, req, api.CreateTripParams{})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
				t.Fatalf("content type = %q", got)
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != "invalid_request" {
				t.Fatalf("code = %q", problem.Code)
			}
		})
	}
}
