package finish

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

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, tripID api.TripId) {
	trip, err := h.usecase.Execute(r.Context(), tripID)
	if err != nil {
		response.Error(w, r, h.logger, err)
		return
	}
	response.JSON(w, http.StatusOK, dto.Trip(trip))
}
