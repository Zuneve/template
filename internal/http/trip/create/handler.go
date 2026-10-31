package create

import (
	"log/slog"
	"net/http"

	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/response"
	"github.com/Zuneve/template/internal/http/trip/dto"
)

type Handler struct {
	usecase usecase
	logger  *slog.Logger
}

func New(usecase usecase, logger *slog.Logger) *Handler {
	return &Handler{usecase: usecase, logger: logger}
}

// Handle answers 201 with a Location header for a new trip, or 200 when the
// Idempotency-Key replays a trip that was created earlier.
func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, params api.CreateTripParams) {
	body, detail := decodeRequest(w, r)
	if detail != "" {
		response.InvalidRequest(w, r, detail)
		return
	}

	out, err := h.usecase.Execute(r.Context(), toInput(body, params.IdempotencyKey))
	if err != nil {
		response.Error(w, r, h.logger, err)
		return
	}

	status := http.StatusOK
	if out.Created {
		status = http.StatusCreated
		w.Header().Set("Location", "/api/v1/trips/"+out.Trip.ID.String())
	}
	response.JSON(w, status, dto.Trip(out.Trip))
}
