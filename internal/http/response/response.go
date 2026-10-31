// Package response writes JSON and problem+json responses.
package response

import (
	"encoding/json"
	"log/slog"
	"net/http"

	api "github.com/Zuneve/template/internal/generated"
	"github.com/Zuneve/template/internal/http/domainerror"
)

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Problem writes an RFC 9457 application/problem+json response; instance is
// filled in from the request path.
func Problem(w http.ResponseWriter, r *http.Request, status int, p api.Problem) {
	instance := r.URL.Path
	p.Instance = &instance

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

func InvalidRequest(w http.ResponseWriter, r *http.Request, detail string) {
	status, p := domainerror.InvalidRequest(detail)
	Problem(w, r, status, p)
}

// Error answers with the problem that domainerror maps err to. The real text
// of an internal error is logged, never sent to the client.
func Error(w http.ResponseWriter, r *http.Request, logger *slog.Logger, err error) {
	status, p := domainerror.Problem(err)
	if status == http.StatusInternalServerError {
		logger.ErrorContext(r.Context(), "internal error", slog.Any("error", err))
	}
	Problem(w, r, status, p)
}
