package push

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

var (
	driverID = uuid.MustParse("8860b315-ec86-42eb-a17c-7c163d721ff5")
	tripID   = uuid.MustParse("1f0a9c62-4a1c-4f2e-9d33-2a4bb0f0b111")
)

func newClient(t *testing.T, url string, timeout time.Duration) *Client {
	t.Helper()

	c, err := New(url, timeout, tracenoop.NewTracerProvider(), propagation.TraceContext{})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

func TestRequestPosition_SendsContractRequest(t *testing.T) {
	type received struct {
		method, path, contentType string
		body                      map[string]any
	}
	got := make(chan received, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		got <- received{r.Method, r.URL.Path, r.Header.Get("Content-Type"), body}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"message_id":"m1","accepted_at":"2026-11-15T12:00:00Z"}`))
	}))
	defer srv.Close()

	err := newClient(t, srv.URL, time.Second).RequestPosition(t.Context(), driverID, tripID)

	require.NoError(t, err)
	r := <-got
	require.Equal(t, http.MethodPost, r.method)
	require.Equal(t, "/api/v1/push", r.path)
	require.Equal(t, "application/json", r.contentType)
	require.Equal(t, "REQUEST_POSITION", r.body["kind"])
	require.Equal(t, driverID.String(), r.body["recipient_id"])
	require.Equal(t, map[string]any{"trip_id": tripID.String()}, r.body["data"])
	require.NotEmpty(t, r.body["request_id"])
}

func TestRequestPosition_BaseURLWithTrailingSlash(t *testing.T) {
	path := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path <- r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	require.NoError(t, newClient(t, srv.URL+"/", time.Second).RequestPosition(t.Context(), driverID, tripID))

	require.Equal(t, "/api/v1/push", <-path)
}

func TestRequestPosition_NonSuccessStatusIsStatusError(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(code)
			}))
			defer srv.Close()

			err := newClient(t, srv.URL, time.Second).RequestPosition(t.Context(), driverID, tripID)

			var statusErr *StatusError
			require.ErrorAs(t, err, &statusErr)
			require.Equal(t, code, statusErr.StatusCode)
			require.NotErrorIs(t, err, ErrTransport, "an HTTP answer is not a transport error")
			require.EqualValues(t, 1, calls.Load(), "a failed call must not be retried")
		})
	}
}

func TestRequestPosition_UnreachableServerIsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	err := newClient(t, url, time.Second).RequestPosition(t.Context(), driverID, tripID)

	require.ErrorIs(t, err, ErrTransport)
	var statusErr *StatusError
	require.False(t, errors.As(err, &statusErr))
}

func TestRequestPosition_SlowServerTimesOut(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	err := newClient(t, srv.URL, 50*time.Millisecond).RequestPosition(t.Context(), driverID, tripID)

	require.ErrorIs(t, err, ErrTransport)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRequestPosition_CancelledContext(t *testing.T) {
	release := make(chan struct{})
	reached := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(reached)
		<-release
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(t.Context())
	errc := make(chan error, 1)
	go func() { errc <- newClient(t, srv.URL, time.Minute).RequestPosition(ctx, driverID, tripID) }()
	<-reached
	cancel()

	err := <-errc
	require.ErrorIs(t, err, ErrTransport)
	require.ErrorIs(t, err, context.Canceled)
}

type closeTrackingBody struct {
	io.Reader
	closed *atomic.Bool
}

func (b closeTrackingBody) Close() error { b.closed.Store(true); return nil }

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRequestPosition_ClosesResponseBody(t *testing.T) {
	for _, code := range []int{http.StatusAccepted, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			var closed atomic.Bool
			c := newClient(t, "http://push.invalid", time.Second)
			c.http.Transport = fakeTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: code,
					Body:       closeTrackingBody{Reader: io.NopCloser(io.LimitReader(zeroReader{}, 10)), closed: &closed},
				}, nil
			})

			_ = c.RequestPosition(t.Context(), driverID, tripID)

			require.True(t, closed.Load(), "the response body must be closed on every status")
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestNew_Validation(t *testing.T) {
	_, err := New("http://localhost:8090", 0, tracenoop.NewTracerProvider(), propagation.TraceContext{})
	require.Error(t, err)
}

func newTracedClient(t *testing.T, url string) (*Client, *tracetest.SpanRecorder) {
	t.Helper()

	spans := tracetest.NewSpanRecorder()
	c, err := New(url, time.Second, sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)), propagation.TraceContext{})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c, spans
}

func TestRequestPosition_PropagatesTraceContextInsideItsOwnSpan(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("traceparent")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	c, spans := newTracedClient(t, srv.URL)

	require.NoError(t, c.RequestPosition(t.Context(), driverID, tripID))

	ended := spans.Ended()
	require.Len(t, ended, 1)
	span := ended[0]
	require.Equal(t, "POST /api/v1/push", span.Name())
	require.Equal(t, codes.Unset, span.Status().Code)
	require.Contains(t, receiveHeader(t, got), span.SpanContext().TraceID().String()+"-"+span.SpanContext().SpanID().String(),
		"the header carries the push span, so the Push Service becomes its child")
}

func receiveHeader(t *testing.T, ch <-chan string) string {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(time.Second):
		t.Fatal("server did not get the request")
		return ""
	}
}

func TestRequestPosition_NonSuccessStatusMarksTheSpan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c, spans := newTracedClient(t, srv.URL)

	require.Error(t, c.RequestPosition(t.Context(), driverID, tripID))

	ended := spans.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, codes.Error, ended[0].Status().Code)
	require.NotEmpty(t, ended[0].Events(), "the error is recorded on the span")
}

func TestRequestPosition_TransportFailureMarksTheSpan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	c, spans := newTracedClient(t, url)

	require.ErrorIs(t, c.RequestPosition(t.Context(), driverID, tripID), ErrTransport)

	ended := spans.Ended()
	require.Len(t, ended, 1)
	require.Equal(t, codes.Error, ended[0].Status().Code)
}
