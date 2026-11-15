//go:generate go tool mockgen -source ${GOFILE} -destination mocks_test.go -package ${GOPACKAGE}_test
package server

import (
	"context"
	"net/http"
	"time"

	api "github.com/Zuneve/template/internal/generated"
)

type (
	createTripHandler interface {
		Handle(w http.ResponseWriter, r *http.Request, params api.CreateTripParams)
	}

	getTripHandler interface {
		Handle(w http.ResponseWriter, r *http.Request, tripID api.TripId)
	}

	finishTripHandler interface {
		Handle(w http.ResponseWriter, r *http.Request, tripID api.TripId)
	}

	savePositionHandler interface {
		Handle(w http.ResponseWriter, r *http.Request, tripID api.TripId)
	}

	listPositionsHandler interface {
		Handle(w http.ResponseWriter, r *http.Request, tripID api.TripId)
	}

	healthHandler interface {
		Live(w http.ResponseWriter, r *http.Request)
		Ready(w http.ResponseWriter, r *http.Request)
	}

	httpMetrics interface {
		RequestStarted(ctx context.Context, method, route string)
		RequestFinished(ctx context.Context, method, route string, status int, duration time.Duration)
	}
)
