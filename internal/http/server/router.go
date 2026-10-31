package server

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/middleware"
	"github.com/Zuneve/template/internal/http/response"
)

// serverInterface implements api.ServerInterface by handing every operation
// to the handler package that owns it. Operations outside this service's
// scope (trip positions) fall through to api.Unimplemented and answer 501.
type serverInterface struct {
	api.Unimplemented

	create createTripHandler
	get    getTripHandler
	finish finishTripHandler
	health healthHandler
}

func (s serverInterface) CreateTrip(w http.ResponseWriter, r *http.Request, params api.CreateTripParams) {
	s.create.Handle(w, r, params)
}

func (s serverInterface) GetTrip(w http.ResponseWriter, r *http.Request, tripID api.TripId) {
	s.get.Handle(w, r, tripID)
}

func (s serverInterface) FinishTrip(w http.ResponseWriter, r *http.Request, tripID api.TripId) {
	s.finish.Handle(w, r, tripID)
}

func (s serverInterface) Health(w http.ResponseWriter, r *http.Request) {
	s.health.Live(w, r)
}

func (s serverInterface) Ready(w http.ResponseWriter, r *http.Request) {
	s.health.Ready(w, r)
}

// Deps are everything the router needs besides the handlers.
type Deps struct {
	Logger         *slog.Logger
	RequestTimeout time.Duration
	TracerProvider trace.TracerProvider
	Propagator     propagation.TextMapPropagator
	Metrics        httpMetrics
}

// NewRouter builds the HTTP router from the generated OpenAPI routes.
// Path-parameter binding failures (e.g. a non-UUID tripId) are reported
// through the same problem+json format as errors from usecases, instead of
// the generated package's plain-text default.
//
// Middleware order is fixed: Recover, Tracing, Logging, Metrics, then the
// router. Recover is outermost, so it answers 500 only after the
// observability middleware have recorded the request.
func NewRouter(
	create createTripHandler,
	get getTripHandler,
	finish finishTripHandler,
	health healthHandler,
	deps Deps,
) http.Handler {
	router := chi.NewRouter()

	route := func(r *http.Request) string {
		rctx := chi.NewRouteContext()
		if !router.Match(rctx, r.Method, r.URL.Path) {
			return ""
		}
		return rctx.RoutePattern()
	}

	router.Use(
		middleware.Recover(deps.Logger),
		middleware.Tracing(deps.TracerProvider, deps.Propagator, route),
		middleware.Logging(deps.Logger, route),
		middleware.Metrics(deps.Metrics, route),
		middleware.Timeout(deps.RequestTimeout),
	)

	return api.HandlerWithOptions(
		serverInterface{create: create, get: get, finish: finish, health: health},
		api.ChiServerOptions{
			BaseRouter: router,
			ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
				response.InvalidRequest(w, r, err.Error())
			},
		},
	)
}
