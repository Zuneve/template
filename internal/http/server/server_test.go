package server_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Zuneve/template/internal/http/server"
)

func TestNew_SetsTimeouts(t *testing.T) {
	handler := http.NotFoundHandler()

	srv := server.New(":8080", handler)

	require.Equal(t, ":8080", srv.Addr)
	require.NotNil(t, srv.Handler)
	require.Positive(t, srv.ReadTimeout)
	require.Positive(t, srv.ReadHeaderTimeout)
	require.Positive(t, srv.WriteTimeout)
	require.Positive(t, srv.IdleTimeout)
}
