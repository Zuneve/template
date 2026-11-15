// Package dto converts domain trips to the API representation.
package dto

import (
	"time"

	domain "github.com/Zuneve/template/internal/domain/trip"
	api "github.com/Zuneve/template/internal/generated"
)

func Trip(t domain.Trip) api.Trip {
	var finishedAt *time.Time
	if t.FinishedAt != nil {
		utc := t.FinishedAt.UTC()
		finishedAt = &utc
	}

	return api.Trip{
		Id:         t.ID,
		UserId:     t.UserID,
		DriverId:   t.DriverID,
		StartPoint: api.Coordinates(t.StartPoint),
		EndPoint:   api.Coordinates(t.EndPoint),
		Price:      t.Price,
		Status:     api.TripStatus(t.Status),
		StartedAt:  t.StartedAt.UTC(),
		FinishedAt: finishedAt,
	}
}

func Position(p domain.Position) api.TripPosition {
	return api.TripPosition{
		Id:          p.ID,
		TripId:      p.TripID,
		Coordinates: api.Coordinates(p.Point),
		RecordedAt:  p.RecordedAt.UTC(),
	}
}

// Positions is never nil inside the response: an empty route must serialize as
// [] and not as null.
func Positions(ps []domain.Position) api.TripPositionsResponse {
	out := make([]api.TripPosition, 0, len(ps))
	for _, p := range ps {
		out = append(out, Position(p))
	}
	return api.TripPositionsResponse{Positions: out}
}
