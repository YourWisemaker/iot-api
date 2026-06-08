package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/YourWisemaker/iot-api/internal/alerts"
	"github.com/YourWisemaker/iot-api/internal/models"
	"github.com/YourWisemaker/iot-api/internal/realtime"
	"github.com/YourWisemaker/iot-api/internal/store"
	"github.com/YourWisemaker/iot-api/internal/worker"
)

// fakePublisher records broadcast events for assertions.
type fakePublisher struct {
	mu     sync.Mutex
	events []models.Event
}

func (f *fakePublisher) Broadcast(e models.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, e)
}

func (f *fakePublisher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

// fakeCache records status writes.
type fakeCache struct {
	mu       sync.Mutex
	statuses map[string]models.DeviceStatus
}

func newFakeCache() *fakeCache {
	return &fakeCache{statuses: map[string]models.DeviceStatus{}}
}

func (c *fakeCache) SetDeviceStatus(_ context.Context, id string, s models.DeviceStatus, _ time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.statuses[id] = s
	return nil
}

func (c *fakeCache) GetDeviceStatus(_ context.Context, id string) (models.DeviceStatus, time.Time, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.statuses[id]
	return s, time.Time{}, ok, nil
}

func newTestService(t *testing.T, opts ...Option) (*Service, *worker.Pool) {
	t.Helper()
	pool := worker.NewPool(4, 64)
	pool.Start()
	t.Cleanup(pool.Stop)

	engine := alerts.NewEngine(
		models.AlertRule{Metric: "temperature", Operator: ">", Threshold: 80, Severity: models.SeverityCritical},
	)
	svc := New(store.NewMemoryStore(100), pool, engine, realtime.NewHub(16), time.Second, opts...)
	return svc, pool
}

// waitFor polls until cond is true or the timeout elapses.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestRegisterDevice(t *testing.T) {
	pub := &fakePublisher{}
	svc, _ := newTestService(t, WithPublisher(pub))

	d, err := svc.RegisterDevice(models.RegisterDeviceRequest{Name: "thermostat", Type: "sensor"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if d.ID == "" {
		t.Fatal("expected generated ID")
	}
	if d.Status != models.StatusUnknown {
		t.Fatalf("expected unknown status, got %s", d.Status)
	}
	if pub.count() != 1 {
		t.Fatalf("expected 1 device event, got %d", pub.count())
	}
}

func TestIngestTelemetryUpdatesStatusAndStores(t *testing.T) {
	pub := &fakePublisher{}
	cache := newFakeCache()
	svc, pool := newTestService(t, WithPublisher(pub), WithStatusCache(cache))

	d, _ := svc.RegisterDevice(models.RegisterDeviceRequest{Name: "d"})

	ok := svc.IngestTelemetry(models.Telemetry{
		DeviceID: d.ID,
		Metrics:  map[string]float64{"temperature": 25},
	})
	if !ok {
		t.Fatal("ingest rejected")
	}

	waitFor(t, func() bool { return pool.Processed() >= 1 })

	got, _ := svc.GetDevice(d.ID)
	if got.Status != models.StatusOnline {
		t.Fatalf("expected online, got %s", got.Status)
	}
	if len(svc.GetTelemetry(d.ID, time.Time{}, 0)) != 1 {
		t.Fatal("expected 1 telemetry point stored")
	}
	cache.mu.Lock()
	cached := cache.statuses[d.ID]
	cache.mu.Unlock()
	if cached != models.StatusOnline {
		t.Fatalf("expected cached online status, got %s", cached)
	}
}

func TestIngestTriggersAlert(t *testing.T) {
	svc, pool := newTestService(t)
	d, _ := svc.RegisterDevice(models.RegisterDeviceRequest{Name: "d"})

	svc.IngestTelemetry(models.Telemetry{
		DeviceID: d.ID,
		Metrics:  map[string]float64{"temperature": 120},
	})
	waitFor(t, func() bool { return pool.Processed() >= 1 })
	waitFor(t, func() bool { return len(svc.ListAlerts(d.ID)) == 1 })

	alert := svc.ListAlerts(d.ID)[0]
	if alert.Severity != models.SeverityCritical {
		t.Fatalf("expected critical alert, got %s", alert.Severity)
	}
}

func TestReconcileMarksOffline(t *testing.T) {
	pub := &fakePublisher{}
	svc, pool := newTestService(t, WithPublisher(pub))
	d, _ := svc.RegisterDevice(models.RegisterDeviceRequest{Name: "d"})

	svc.IngestTelemetry(models.Telemetry{DeviceID: d.ID, Metrics: map[string]float64{"temperature": 20}})
	waitFor(t, func() bool { return pool.Processed() >= 1 })

	// offlineAfter is 1s in the test service; wait it out then reconcile.
	time.Sleep(1100 * time.Millisecond)
	svc.ReconcileStatuses()

	got, _ := svc.GetDevice(d.ID)
	if got.Status != models.StatusOffline {
		t.Fatalf("expected offline, got %s", got.Status)
	}
}

func TestGetDeviceStatusOverlaidFromCache(t *testing.T) {
	cache := newFakeCache()
	svc, _ := newTestService(t, WithStatusCache(cache))
	d, _ := svc.RegisterDevice(models.RegisterDeviceRequest{Name: "d"})

	// Directly seed a cached status that differs from the stored one.
	_ = cache.SetDeviceStatus(context.Background(), d.ID, models.StatusDegraded, time.Time{})

	got, _ := svc.GetDevice(d.ID)
	if got.Status != models.StatusDegraded {
		t.Fatalf("expected cache to override status, got %s", got.Status)
	}
}
