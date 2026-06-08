package store

import (
	"sync"
	"testing"
	"time"

	"github.com/YourWisemaker/iot-api/internal/models"
)

func newDevice(id string) models.Device {
	return models.Device{
		ID:           id,
		Name:         "dev-" + id,
		Type:         "sensor",
		Status:       models.StatusUnknown,
		RegisteredAt: time.Now().UTC(),
	}
}

func TestCreateAndGetDevice(t *testing.T) {
	s := NewMemoryStore(0)
	d := newDevice("a")
	if err := s.CreateDevice(d); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetDevice("a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "a" {
		t.Fatalf("expected id a, got %s", got.ID)
	}
}

func TestCreateDuplicateDevice(t *testing.T) {
	s := NewMemoryStore(0)
	d := newDevice("a")
	_ = s.CreateDevice(d)
	if err := s.CreateDevice(d); err == nil {
		t.Fatal("expected error on duplicate create")
	}
}

func TestGetMissingDevice(t *testing.T) {
	s := NewMemoryStore(0)
	if _, err := s.GetDevice("missing"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateDeviceStatus(t *testing.T) {
	s := NewMemoryStore(0)
	_ = s.CreateDevice(newDevice("a"))
	seen := time.Now().UTC()
	if err := s.UpdateDeviceStatus("a", models.StatusOnline, seen); err != nil {
		t.Fatalf("update: %v", err)
	}
	d, _ := s.GetDevice("a")
	if d.Status != models.StatusOnline {
		t.Fatalf("expected online, got %s", d.Status)
	}
	if !d.LastSeenAt.Equal(seen) {
		t.Fatalf("last seen not updated")
	}
}

func TestDeleteDevice(t *testing.T) {
	s := NewMemoryStore(0)
	_ = s.CreateDevice(newDevice("a"))
	if err := s.DeleteDevice("a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetDevice("a"); err != ErrNotFound {
		t.Fatal("device should be gone")
	}
	if err := s.DeleteDevice("a"); err != ErrNotFound {
		t.Fatal("expected ErrNotFound deleting twice")
	}
}

func TestAddTelemetryUnknownDevice(t *testing.T) {
	s := NewMemoryStore(0)
	err := s.AddTelemetry(models.Telemetry{DeviceID: "ghost", Timestamp: time.Now()})
	if err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestTelemetryHistoryCap(t *testing.T) {
	s := NewMemoryStore(3)
	_ = s.CreateDevice(newDevice("a"))
	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		_ = s.AddTelemetry(models.Telemetry{
			DeviceID:  "a",
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Metrics:   map[string]float64{"v": float64(i)},
		})
	}
	got := s.GetTelemetry("a", time.Time{}, 0)
	if len(got) != 3 {
		t.Fatalf("expected cap of 3, got %d", len(got))
	}
	if got[0].Metrics["v"] != 2 {
		t.Fatalf("expected oldest retained value 2, got %v", got[0].Metrics["v"])
	}
}

func TestGetTelemetrySinceAndLimit(t *testing.T) {
	s := NewMemoryStore(0)
	_ = s.CreateDevice(newDevice("a"))
	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		_ = s.AddTelemetry(models.Telemetry{
			DeviceID:  "a",
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			Metrics:   map[string]float64{"v": float64(i)},
		})
	}
	since := base.Add(2 * time.Minute)
	got := s.GetTelemetry("a", since, 0)
	if len(got) != 3 {
		t.Fatalf("expected 3 points since, got %d", len(got))
	}

	limited := s.GetTelemetry("a", time.Time{}, 2)
	if len(limited) != 2 {
		t.Fatalf("expected 2 limited, got %d", len(limited))
	}
	if limited[len(limited)-1].Metrics["v"] != 4 {
		t.Fatalf("expected newest value 4, got %v", limited[len(limited)-1].Metrics["v"])
	}
}

func TestListAlertsFilter(t *testing.T) {
	s := NewMemoryStore(0)
	_ = s.AddAlert(models.Alert{ID: "1", DeviceID: "a", CreatedAt: time.Now()})
	_ = s.AddAlert(models.Alert{ID: "2", DeviceID: "b", CreatedAt: time.Now()})
	if got := s.ListAlerts("a"); len(got) != 1 {
		t.Fatalf("expected 1 alert for a, got %d", len(got))
	}
	if got := s.ListAlerts(""); len(got) != 2 {
		t.Fatalf("expected 2 alerts total, got %d", len(got))
	}
}

// TestConcurrentAccess exercises the store under concurrent reads and writes;
// run with -race to detect data races.
func TestConcurrentAccess(t *testing.T) {
	s := NewMemoryStore(100)
	_ = s.CreateDevice(newDevice("a"))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = s.AddTelemetry(models.Telemetry{
				DeviceID:  "a",
				Timestamp: time.Now().UTC(),
				Metrics:   map[string]float64{"v": float64(n)},
			})
			_ = s.GetTelemetry("a", time.Time{}, 0)
			_, _ = s.GetDevice("a")
			_ = s.ListDevices()
		}(i)
	}
	wg.Wait()

	if got := s.GetTelemetry("a", time.Time{}, 0); len(got) == 0 {
		t.Fatal("expected telemetry to be recorded")
	}
}
