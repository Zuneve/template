// Package push is the HTTP client of the Push Service: it turns "ask this
// driver's device for a position" into one POST /api/v1/push call.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName       = "github.com/Zuneve/template/internal/gateway/push"
	spanName         = "POST " + pushPath
	pushPath         = "/api/v1/push"
	kindRequestPos   = "REQUEST_POSITION"
	maxDrainedBody   = 64 << 10
	dialTimeout      = 500 * time.Millisecond
	tlsTimeout       = 500 * time.Millisecond
	idleConnTimeout  = 30 * time.Second
	maxIdlePerHost   = 16
	maxIdleConnsAll  = 64
	expectContinueTO = time.Second
)

// ErrTransport marks a call that did not get an HTTP answer at all: refused or
// reset connection, DNS failure, timeout, cancelled context. The underlying
// error stays reachable, so errors.Is(err, context.DeadlineExceeded) still works.
var ErrTransport = errors.New("push transport error")

// StatusError is an HTTP answer other than 2xx: the Push Service was reached
// and said no (4xx) or failed (5xx).
type StatusError struct {
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("push service answered %d", e.StatusCode)
}

// Client talks to one Push Service. It owns its http.Client and never
// retries: a failed call is reported once and the next scanner tick decides
// whether to try again.
type Client struct {
	endpoint   string
	http       *http.Client
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
}

// New builds a client for the Push Service at baseURL. timeout caps one whole
// call, from dial to the last byte of the response. Every call gets its own
// client span, and the propagator puts the trace context into the request
// headers so the Push Service can continue the trace.
func New(
	baseURL string,
	timeout time.Duration,
	tracers trace.TracerProvider,
	propagator propagation.TextMapPropagator,
) (*Client, error) {
	endpoint, err := url.JoinPath(baseURL, pushPath)
	if err != nil {
		return nil, fmt.Errorf("build push endpoint from %q: %w", baseURL, err)
	}
	if timeout <= 0 {
		return nil, errors.New("push timeout must be positive")
	}

	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   tlsTimeout,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: expectContinueTO,
		MaxIdleConns:          maxIdleConnsAll,
		MaxIdleConnsPerHost:   maxIdlePerHost,
		IdleConnTimeout:       idleConnTimeout,
	}
	return &Client{
		endpoint:   endpoint,
		http:       &http.Client{Transport: transport, Timeout: timeout},
		tracer:     tracers.Tracer(tracerName),
		propagator: propagator,
	}, nil
}

type pushRequest struct {
	RequestID   string            `json:"request_id"`
	RecipientID uuid.UUID         `json:"recipient_id"`
	Kind        string            `json:"kind"`
	Data        map[string]string `json:"data,omitempty"`
}

// RequestPosition asks the device of driverID to send a fresh position of
// tripID. nil means the Push Service accepted the command, not that the device
// answered. Failures are ErrTransport (no answer) or *StatusError (non-2xx).
func (c *Client) RequestPosition(ctx context.Context, driverID, tripID uuid.UUID) (err error) {
	ctx, span := c.tracer.Start(ctx, spanName, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.String("http.request.method", http.MethodPost),
		attribute.String("url.full", c.endpoint),
		attribute.String("trip_id", tripID.String()),
	))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	body, err := json.Marshal(pushRequest{
		RequestID:   uuid.NewString(),
		RecipientID: driverID,
		Kind:        kindRequestPos,
		Data:        map[string]string{"trip_id": tripID.String()},
	})
	if err != nil {
		return fmt.Errorf("encode push request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.propagator.Inject(ctx, propagation.HeaderCarrier(req.Header))

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTransport, err)
	}
	// Reading the rest of a small body lets the connection go back to the pool.
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainedBody))
		_ = resp.Body.Close()
	}()

	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &StatusError{StatusCode: resp.StatusCode}
	}
	return nil
}

// Close drops idle connections; call it after the workers have stopped.
func (c *Client) Close() {
	c.http.CloseIdleConnections()
}
