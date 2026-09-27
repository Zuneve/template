package httpapi

import (
	"net/http"
	"time"
)

// Timeouts are fixed operational parameters, not something that varies
// between environments, so they aren't read from config.
const (
	readTimeout       = 5 * time.Second
	readHeaderTimeout = 5 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 60 * time.Second
)

// NewServer builds an http.Server with timeouts set: without them a single
// stalled connection holds its goroutine and file descriptor forever.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       readTimeout,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}
