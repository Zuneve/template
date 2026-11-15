package middleware

import (
	"log/slog"
	"net/http"
	"time"
)

// Logging writes one record per request, after the response is ready. It logs
// neither bodies (personal data) nor the concrete path (the route template is
// enough). Only 5xx is an Error: a 4xx is the client's mistake.
func Logging(logger *slog.Logger, route RouteResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			routeName := route(r)
			rec := recordStatus(w)

			completed := false
			defer func() {
				status := rec.statusOf(completed)
				level := slog.LevelInfo
				if status >= http.StatusInternalServerError {
					level = slog.LevelError
				}

				logger.LogAttrs(r.Context(), level, "request handled",
					slog.String("method", r.Method),
					slog.String("route", routeName),
					slog.Int("status", status),
					slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				)
			}()

			next.ServeHTTP(rec, r)
			completed = true
		})
	}
}
