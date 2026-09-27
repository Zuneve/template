package httpapi

import (
	"net/http"

	api "github.com/Zuneve/template/internal/generated"
)

// Health reports 200 as long as the process is alive; it never touches the
// database (that's what Ready is for). Errors here aren't problem+json: the
// contract says the orchestrator only reads the status code.
func (a *API) Health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

func (a *API) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := a.withQueryTimeout(r.Context())
	defer cancel()
	if err := a.ready(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, api.HealthResponse{Status: api.Unavailable})
		return
	}
	writeJSON(w, http.StatusOK, api.HealthResponse{Status: api.Ok})
}
