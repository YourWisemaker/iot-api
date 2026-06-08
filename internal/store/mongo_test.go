package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// mongoTestStore connects to the MongoDB instance named by MONGO_TEST_URI.
// When the variable is unset the test is skipped, keeping the default test run
// hermetic. To run it: MONGO_TEST_URI=mongodb://localhost:27017 go test ./...
func mongoTestStore(t *testing.T) *MongoStore {
	t.Helper()
	uri := os.Getenv("MONGO_TEST_URI")
	if uri == "" {
		t.Skip("set MONGO_TEST_URI to run MongoDB integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	s, err := NewMongoStore(ctx, MongoConfig{
		URI:        uri,
		Database:   "iot_test",
		MaxHistory: 3,
	})
	if err != nil {
		t.Fatalf("connect mongo: %v", err)
	}
	// Start from a clean slate.
	_, _ = s.devices.DeleteMany(ctx, map[string]any{})
	_, _ = s.telemetry.DeleteMany(ctx, map[string]any{})
	_, _ = s.alerts.DeleteMany(ctx, map[string]any{})

	t.Cleanup(func() {
		_ = s.Close(context.Background())
	})
	return s
}

func TestMongoDeviceLifecycle(t *testing.T) {
	s := mongoTestStore(t)
	d := models.Device{
		ID:           "m1",
		Name:         "mongo-device",
		Type:         "sensor",
		Status:       models.StatusUnknown,
		RegisteredAt: time.Now().UTC(),
	}
	if err := s.CreateDevice(d); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.CreateDevice(d); err == nil {
		t.Fatal("expected duplicate error")
	}

	got, err := s.GetDevice("m1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "mongo-device" {
		t.Fatalf("unexpected device: %+v", got)
	}

	seen := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.UpdateDeviceStatus("m1", models.StatusOnline, seen); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = s.GetDevice("m1")
	if got.Status != models.StatusOnline {
		t.Fatalf("expected online, got %s", got.Status)
	}

	if err := s.DeleteDevice("m1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetDevice("m1"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMongoTelemetryHistoryCap(t *testing.T) {
	s := mongoTestStore(t)
	_ = s.CreateDevice(models.Device{ID: "m2", Name: "d", RegisteredAt: time.Now().UTC()})

	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if err := s.AddTelemetry(models.Telemetry{
			DeviceID:  "m2",
			Timestamp: base.Add(time.Duration(i) * time.Second),
			Metrics:   map[string]float64{"v": float64(i)},
		}); err != nil {
			t.Fatalf("add telemetry: %v", err)
		}
	}

	got := s.GetTelemetry("m2", time.Time{}, 0)
	if len(got) != 3 {
		t.Fatalf("expected history cap 3, got %d", len(got))
	}
	if got[0].Metrics["v"] != 2 {
		t.Fatalf("expected oldest retained value 2, got %v", got[0].Metrics["v"])
	}
}

func TestMongoAlerts(t *testing.T) {
	s := mongoTestStore(t)
	_ = s.AddAlert(models.Alert{ID: "a1", DeviceID: "m3", Metric: "temp", CreatedAt: time.Now().UTC()})
	_ = s.AddAlert(models.Alert{ID: "a2", DeviceID: "other", CreatedAt: time.Now().UTC()})

	if got := s.ListAlerts("m3"); len(got) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(got))
	}
	if got := s.ListAlerts(""); len(got) != 2 {
		t.Fatalf("expected 2 alerts, got %d", len(got))
	}
}
