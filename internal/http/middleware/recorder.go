package middleware

import "net/http"

// statusRecorder remembers the status code a handler wrote. A handler that
// writes a body without calling WriteHeader answers 200, as net/http does.
type statusRecorder struct {
	http.ResponseWriter

	status int
}

// recordStatus wraps w once: the three observability middlewares share one
// recorder instead of stacking three.
func recordStatus(w http.ResponseWriter) *statusRecorder {
	if rec, ok := w.(*statusRecorder); ok {
		return rec
	}
	return &statusRecorder{ResponseWriter: w}
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// statusOf reports the recorded status. When the handler never returned
// (a panic is unwinding through the middleware), nothing was answered yet and
// Recover is about to answer 500.
func (r *statusRecorder) statusOf(completed bool) int {
	switch {
	case !completed:
		return http.StatusInternalServerError
	case r.status == 0:
		return http.StatusOK
	default:
		return r.status
	}
}
