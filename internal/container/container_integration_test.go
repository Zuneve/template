//go:build integration

package container_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/Zuneve/template/internal/config"
	"github.com/Zuneve/template/internal/container"
	domain "github.com/Zuneve/template/internal/domain/trip"
	"github.com/Zuneve/template/internal/infra/telemetry"
	triprepo "github.com/Zuneve/template/internal/repository/trip"
	"github.com/Zuneve/template/internal/testutil"
)

const (
	waitLimit    = 10 * time.Second
	checkEvery   = 50 * time.Millisecond
	staleHorizon = time.Hour
)

// fakePush is the Push Service: it remembers which trips it was asked about.
type fakePush struct {
	*httptest.Server

	mu      sync.Mutex
	pushed  map[string]int
	drivers map[string]string
}

func newFakePush(t *testing.T) *fakePush {
	t.Helper()

	f := &fakePush{pushed: map[string]int{}, drivers: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RecipientID string            `json:"recipient_id"`
			Kind        string            `json:"kind"`
			Data        map[string]string `json:"data"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		f.mu.Lock()
		f.pushed[body.Data["trip_id"]]++
		f.drivers[body.Data["trip_id"]] = body.RecipientID
		f.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakePush) count(tripID uuid.UUID) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pushed[tripID.String()]
}

func (f *fakePush) driver(tripID uuid.UUID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.drivers[tripID.String()]
}

func startContainer(t *testing.T, pushURL string) *container.Container {
	t.Helper()

	t.Setenv("POSITION_CHECK_INTERVAL", checkEvery.String())
	t.Setenv("POSITION_STALE_AFTER", staleHorizon.String())
	t.Setenv("PUSH_HTTP_URL", pushURL)
	cfg, err := config.Load()
	require.NoError(t, err)

	logger := telemetry.NewLogger(testWriter{t}, cfg.LogLevel)
	tel, err := telemetry.Setup(t.Context(), "test", logger)
	require.NoError(t, err)

	app, err := container.New(t.Context(), cfg, logger, tel)
	require.NoError(t, err)

	app.StartBackground(context.WithoutCancel(t.Context()))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitLimit)
		defer cancel()
		require.NoError(t, app.StopBackground(ctx))
		require.NoError(t, tel.Shutdown(ctx))
		app.Close()
	})
	return app
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func createStaleTrip(t *testing.T) domain.Trip {
	t.Helper()

	pool := testutil.Pool(t)
	tr, err := domain.New(domain.NewParams{
		ID:         uuid.New(),
		UserID:     uuid.New(),
		DriverID:   uuid.New(),
		StartPoint: domain.Point{Latitude: 55.75, Longitude: 37.61},
		EndPoint:   domain.Point{Latitude: 59.93, Longitude: 30.33},
		Price:      1500,
		StartedAt:  time.Now().UTC().Add(-2 * staleHorizon).Truncate(time.Microsecond),
	})
	require.NoError(t, err)
	testutil.CleanupTrips(t, pool, tr.ID)
	require.NoError(t, triprepo.New(pool).Create(t.Context(), tr))
	return tr
}

func TestBackground_StaleTripIsPushedAndAPositionStopsIt(t *testing.T) {
	push := newFakePush(t)
	trip := createStaleTrip(t)
	clock := createStaleTrip(t)
	app := startContainer(t, push.URL)

	require.Eventually(t, func() bool { return push.count(trip.ID) > 0 }, waitLimit, 10*time.Millisecond,
		"the silent trip must be pushed")
	require.Equal(t, trip.DriverID.String(), push.driver(trip.ID), "the push goes to the trip's driver")

	body := `{"coordinates":{"latitude":59.93,"longitude":30.33},"recorded_at":"` + time.Now().UTC().Format(time.RFC3339Nano) + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/trips/"+trip.ID.String()+"/positions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	// The second silent trip is a clock that needs no sleeping: every push to it
	// means one more scanner pass. A push of the first trip that was already
	// queued or in flight when the position arrived may still land, so the
	// baseline is taken only after two further passes; after that the first
	// trip is fresh and must stay untouched for three more.
	sinceSent := push.count(clock.ID)
	waitForPushes(t, push, clock.ID, sinceSent+2)
	settled := push.count(trip.ID)
	waitForPushes(t, push, clock.ID, push.count(clock.ID)+3)
	require.Equal(t, settled, push.count(trip.ID), "a trip that reported a position is not pushed any more")
}

func waitForPushes(t *testing.T, push *fakePush, tripID uuid.UUID, atLeast int) {
	t.Helper()

	require.Eventually(t, func() bool { return push.count(tripID) >= atLeast }, waitLimit, 5*time.Millisecond,
		"the scanner must keep polling the silent trip")
}

func TestBackground_ShutdownLeavesNoGoroutines(t *testing.T) {
	push := newFakePush(t)
	before := goleak.IgnoreCurrent()

	t.Run("run", func(t *testing.T) {
		trip := createStaleTrip(t)
		startContainer(t, push.URL)
		require.Eventually(t, func() bool { return push.count(trip.ID) > 0 }, waitLimit, 10*time.Millisecond)
	})

	push.Close()
	goleak.VerifyNone(t, before)
}
