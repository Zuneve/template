package middleware

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/Zuneve/template/internal/http/domainerror"
	"github.com/Zuneve/template/internal/http/response"
)

// Recover turns a panic in a handler into a logged 500 internal_error, so the
// process keeps serving.
func Recover(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec == nil {
					return
				}
				// net/http uses this value to abort a response on purpose.
				if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(rec)
				}

				logger.ErrorContext(r.Context(), "panic recovered",
					slog.String("panic", fmt.Sprint(rec)),
					slog.String("stack", string(debug.Stack())),
				)
				status, p := domainerror.InternalError()
				response.Problem(w, r, status, p)
			}()

			next.ServeHTTP(w, r)
		})
	}
}
