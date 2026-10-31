package health

import (
	"net/http"

	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/response"
)

type Handler struct {
	db pinger
}

func New(db pinger) *Handler {
	return &Handler{db: db}
}

// Live reports 200 as long as the process is alive; it never touches the
// database (that is what Ready is for). The contract says the orchestrator
// only reads the status code, so errors here are not problem+json.
func (h *Handler) Live(w http.ResponseWriter, _ *http.Request) {
	response.JSON(w, http.StatusOK, api.HealthResponse{Status: api.Ok})
}

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	if err := h.db.Ping(r.Context()); err != nil {
		response.JSON(w, http.StatusServiceUnavailable, api.HealthResponse{Status: api.Unavailable})
		return
	}
	response.JSON(w, http.StatusOK, api.HealthResponse{Status: api.Ok})
}
