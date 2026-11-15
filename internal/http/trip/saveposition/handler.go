package saveposition

import (
	"log/slog"
	"net/http"

	domain "github.com/Zuneve/template/internal/domain/trip"
	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/response"
	"github.com/Zuneve/template/internal/http/trip/dto"
	saveusecase "github.com/Zuneve/template/internal/usecase/trip/saveposition"
)

type Handler struct {
	usecase usecase
	logger  *slog.Logger
}

func New(usecase usecase, logger *slog.Logger) *Handler {
	return &Handler{usecase: usecase, logger: logger}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, tripID api.TripId) {
	body, detail := decodeRequest(w, r)
	if detail != "" {
		response.InvalidRequest(w, r, detail)
		return
	}

	saved, err := h.usecase.Execute(r.Context(), saveusecase.Input{
		TripID:     tripID,
		Point:      domain.Point(body.Coordinates),
		RecordedAt: body.RecordedAt,
	})
	if err != nil {
		response.Error(w, r, h.logger, err)
		return
	}
	response.JSON(w, http.StatusCreated, dto.Position(saved))
}
