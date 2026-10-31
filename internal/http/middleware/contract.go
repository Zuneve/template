package middleware

import (
	"context"
	"net/http"
	"time"
)

// RouteResolver returns the route template a request will be served by, for
// example "/api/v1/trips/{tripId}", or "" when no route matches. It is
// resolved before the router runs because spans and in-flight counters are
// opened before the handler is chosen; both need the template rather than
// the concrete path.
type RouteResolver func(r *http.Request) string

type httpMetrics interface {
	RequestStarted(ctx context.Context, method, route string)
	RequestFinished(ctx context.Context, method, route string, status int, duration time.Duration)
}
