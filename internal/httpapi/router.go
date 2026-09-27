package httpapi

import (
	"net/http"

	api "github.com/Zuneve/template/internal/generated"
)

// NewRouter builds the HTTP router from the generated OpenAPI server
// interface. Path-parameter binding failures (e.g. a non-UUID tripId) are
// reported through the same problem+json format as domain errors, instead
// of the generated package's plain-text default.
func NewRouter(impl api.ServerInterface) http.Handler {
	return api.HandlerWithOptions(impl, api.ChiServerOptions{
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, r, http.StatusBadRequest, problemInvalidRequest(err.Error()))
		},
	})
}
