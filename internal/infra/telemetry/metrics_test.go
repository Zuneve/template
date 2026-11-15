package telemetry_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/Zuneve/template/internal/infra/telemetry"
)

const (
	methodGet  = "GET"
	attrMethod = "http.request.method"
	attrRoute  = "http.route"
	attrStatus = "http.response.status_code"
)

func newMetrics(t *testing.T) (*telemetry.Metrics, *sdkmetric.ManualReader) {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	m, err := telemetry.NewMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	require.NoError(t, err)
	return m, reader
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	got := map[string]metricdata.Metrics{}
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			got[m.Name] = m
		}
	}
	return got
}

func attrs(set attribute.Set) map[string]string {
	out := map[string]string{}
	for _, kv := range set.ToSlice() {
		out[string(kv.Key)] = kv.Value.String()
	}
	return out
}

func TestMetrics_Requests(t *testing.T) {
	m, reader := newMetrics(t)
	ctx := context.Background()
	const route = "/api/v1/trips/{tripId}"

	m.RequestStarted(ctx, methodGet, route)
	m.RequestStarted(ctx, methodGet, route)
	m.RequestFinished(ctx, methodGet, route, 200, 30*time.Millisecond)

	metrics := collect(t, reader)

	active := metrics["http.server.active_requests"].Data.(metricdata.Sum[int64])
	require.Len(t, active.DataPoints, 1)
	require.Equal(t, int64(1), active.DataPoints[0].Value, "two started, one finished")
	require.Equal(t, map[string]string{attrMethod: methodGet, attrRoute: route}, attrs(active.DataPoints[0].Attributes))

	duration := metrics["http.server.request.duration"]
	require.Equal(t, "s", duration.Unit)
	hist := duration.Data.(metricdata.Histogram[float64])
	require.Len(t, hist.DataPoints, 1)
	point := hist.DataPoints[0]
	require.Equal(t, uint64(1), point.Count)
	require.InDelta(t, 0.03, point.Sum, 1e-9)
	require.Equal(t, telemetry.DurationBuckets, point.Bounds)
	require.Equal(t, map[string]string{attrMethod: methodGet, attrRoute: route, attrStatus: "200"}, attrs(point.Attributes))
}

func TestMetrics_RequestWithoutRouteHasNoRouteLabel(t *testing.T) {
	m, reader := newMetrics(t)

	m.RequestFinished(context.Background(), methodGet, "", 404, time.Millisecond)

	hist := collect(t, reader)["http.server.request.duration"].Data.(metricdata.Histogram[float64])
	require.Equal(t, map[string]string{attrMethod: methodGet, attrStatus: "404"}, attrs(hist.DataPoints[0].Attributes))
}

func TestMetrics_TripCounters(t *testing.T) {
	m, reader := newMetrics(t)
	ctx := context.Background()

	m.TripCreated(ctx)
	m.TripCreated(ctx)
	m.TripCompleted(ctx)

	metrics := collect(t, reader)
	require.Equal(t, int64(2), metrics["tripgo.trips.created"].Data.(metricdata.Sum[int64]).DataPoints[0].Value)
	require.Equal(t, int64(1), metrics["tripgo.trips.completed"].Data.(metricdata.Sum[int64]).DataPoints[0].Value)
}

func TestMetrics_PositionsSaved(t *testing.T) {
	m, reader := newMetrics(t)
	ctx := context.Background()

	m.PositionSaved(ctx)
	m.PositionSaved(ctx)

	sum := collect(t, reader)["tripgo.positions.saved"].Data.(metricdata.Sum[int64])
	require.Equal(t, int64(2), sum.DataPoints[0].Value)
}

func TestMetrics_StaleChecks(t *testing.T) {
	m, reader := newMetrics(t)
	ctx := context.Background()

	m.StaleCheck(ctx, "queued")
	m.StaleCheck(ctx, "queued")
	m.StaleCheck(ctx, "duplicate")
	m.StaleCheck(ctx, "dropped")

	got := map[string]int64{}
	for _, dp := range collect(t, reader)["tripgo.stale_checks"].Data.(metricdata.Sum[int64]).DataPoints {
		got[attrs(dp.Attributes)["result"]] = dp.Value
	}
	require.Equal(t, map[string]int64{"queued": 2, "duplicate": 1, "dropped": 1}, got)
}

func TestMetrics_PositionRequests(t *testing.T) {
	m, reader := newMetrics(t)
	ctx := context.Background()

	m.PositionRequestFinished(ctx, "success", 50*time.Millisecond)
	m.PositionRequestFinished(ctx, "error", 2*time.Second)
	m.PositionRequestDropped(ctx)
	m.PositionRequestDropped(ctx)

	metrics := collect(t, reader)
	counts := map[string]int64{}
	for _, dp := range metrics["tripgo.position_requests"].Data.(metricdata.Sum[int64]).DataPoints {
		counts[attrs(dp.Attributes)["result"]] = dp.Value
	}
	require.Equal(t, map[string]int64{"success": 1, "error": 1, "dropped": 2}, counts)

	// Only calls that reached the Push Service have a duration; a drop does not.
	durations := map[string]uint64{}
	for _, dp := range metrics["tripgo.position_requests.duration"].Data.(metricdata.Histogram[float64]).DataPoints {
		durations[attrs(dp.Attributes)["result"]] = dp.Count
	}
	require.Equal(t, map[string]uint64{"success": 1, "error": 1}, durations)
}

func TestMetrics_PositionRequestGauges(t *testing.T) {
	m, reader := newMetrics(t)
	queue, active := int64(7), int64(2)
	require.NoError(t, m.ObservePositionRequests(func() int64 { return queue }, func() int64 { return active }))

	metrics := collect(t, reader)
	require.Equal(t, int64(7), metrics["tripgo.position_requests.queue.size"].Data.(metricdata.Gauge[int64]).DataPoints[0].Value)
	require.Equal(t, int64(2), metrics["tripgo.position_requests.workers.active"].Data.(metricdata.Gauge[int64]).DataPoints[0].Value)

	queue, active = 0, 4
	metrics = collect(t, reader)
	require.Equal(t, int64(0), metrics["tripgo.position_requests.queue.size"].Data.(metricdata.Gauge[int64]).DataPoints[0].Value)
	require.Equal(t, int64(4), metrics["tripgo.position_requests.workers.active"].Data.(metricdata.Gauge[int64]).DataPoints[0].Value)
}

func TestMetrics_ActiveTripsGauge(t *testing.T) {
	t.Run("reports the count at collection time", func(t *testing.T) {
		m, reader := newMetrics(t)
		n := int64(3)
		require.NoError(t, m.ObserveActiveTrips(func(context.Context) (int64, error) { return n, nil }))

		require.Equal(t, int64(3), collect(t, reader)["tripgo.trips.active"].Data.(metricdata.Gauge[int64]).DataPoints[0].Value)

		n = 5
		require.Equal(t, int64(5), collect(t, reader)["tripgo.trips.active"].Data.(metricdata.Gauge[int64]).DataPoints[0].Value)
	})

	t.Run("queries the database outside any sampled trace", func(t *testing.T) {
		m, reader := newMetrics(t)
		var sampled bool
		require.NoError(t, m.ObserveActiveTrips(func(ctx context.Context) (int64, error) {
			_, hasDeadline := ctx.Deadline()
			require.True(t, hasDeadline)
			sampled = traceSampled(ctx)
			return 0, nil
		}))

		collect(t, reader)

		require.False(t, sampled)
	})

	t.Run("a failing count reports nothing and does not break other metrics", func(t *testing.T) {
		m, reader := newMetrics(t)
		require.NoError(t, m.ObserveActiveTrips(func(context.Context) (int64, error) { return 0, errors.New("db down") }))
		m.TripCreated(context.Background())

		var rm metricdata.ResourceMetrics
		err := reader.Collect(context.Background(), &rm)

		require.ErrorContains(t, err, "count active trips")
		var names []string
		for _, s := range rm.ScopeMetrics {
			for _, mt := range s.Metrics {
				names = append(names, mt.Name)
			}
		}
		require.Contains(t, names, "tripgo.trips.created")
	})
}
