package middleware

import (
	"net/http"
	"time"
)

// Metrics records http.server.request.duration and keeps
// http.server.active_requests current.
func Metrics(metrics httpMetrics, route RouteResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			routeName := route(r)
			rec := recordStatus(w)

			metrics.RequestStarted(r.Context(), r.Method, routeName)

			completed := false
			defer func() {
				metrics.RequestFinished(r.Context(), r.Method, routeName, rec.statusOf(completed), time.Since(start))
			}()

			next.ServeHTTP(rec, r)
			completed = true
		})
	}
}
