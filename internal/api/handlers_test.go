package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/YourWisemaker/iot-api/internal/alerts"
	"github.com/YourWisemaker/iot-api/internal/models"
	"github.com/YourWisemaker/iot-api/internal/realtime"
	"github.com/YourWisemaker/iot-api/internal/service"
	"github.com/YourWisemaker/iot-api/internal/store"
	"github.com/YourWisemaker/iot-api/internal/worker"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	pool := worker.NewPool(4, 64)
	pool.Start()
	t.Cleanup(pool.Stop)

	engine := alerts.NewEngine(
		models.AlertRule{Metric: "temperature", Operator: ">", Threshold: 80, Severity: models.SeverityCritical},
	)
	svc := service.New(store.NewMemoryStore(100), pool, engine, realtime.NewHub(16), time.Second)
	srv := httptest.NewServer(NewHandler(svc).Routes())
	t.Cleanup(srv.Close)
	return srv
}

func registerDevice(t *testing.T, base string) models.Device {
	t.Helper()
	body := `{"name":"sensor-1","type":"temp"}`
	resp, err := http.Post(base+"/api/devices", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post device: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var d models.Device
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return d
}

func TestHealth(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRegisterAndGetDevice(t *testing.T) {
	srv := newTestServer(t)
	d := registerDevice(t, srv.URL)

	resp, err := http.Get(srv.URL + "/api/devices/" + d.ID)
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestRegisterValidation(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Post(srv.URL+"/api/devices", "application/json", strings.NewReader(`{"type":"x"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing name, got %d", resp.StatusCode)
	}
}

func TestGetMissingDevice(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/api/devices/does-not-exist")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestIngestTelemetryEndpoint(t *testing.T) {
	srv := newTestServer(t)
	d := registerDevice(t, srv.URL)

	body := `{"metrics":{"temperature":42.5}}`
	resp, err := http.Post(srv.URL+"/api/devices/"+d.ID+"/telemetry", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post telemetry: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", resp.StatusCode)
	}
}

func TestIngestTelemetryValidation(t *testing.T) {
	srv := newTestServer(t)
	d := registerDevice(t, srv.URL)
	resp, err := http.Post(srv.URL+"/api/devices/"+d.ID+"/telemetry", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty metrics, got %d", resp.StatusCode)
	}
}

func TestDeleteDevice(t *testing.T) {
	srv := newTestServer(t)
	d := registerDevice(t, srv.URL)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/devices/"+d.ID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestTelemetryAndAnalyticsFlow(t *testing.T) {
	srv := newTestServer(t)
	d := registerDevice(t, srv.URL)

	for _, v := range []float64{10, 20, 30} {
		body, _ := json.Marshal(map[string]any{"metrics": map[string]float64{"temperature": v}})
		resp, err := http.Post(srv.URL+"/api/devices/"+d.ID+"/telemetry", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("post telemetry: %v", err)
		}
		resp.Body.Close()
	}

	// Telemetry is processed asynchronously; poll until it lands.
	var points []models.Telemetry
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(srv.URL + "/api/devices/" + d.ID + "/telemetry")
		if err != nil {
			t.Fatalf("get telemetry: %v", err)
		}
		_ = json.NewDecoder(resp.Body).Decode(&points)
		resp.Body.Close()
		if len(points) == 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(points) != 3 {
		t.Fatalf("expected 3 telemetry points, got %d", len(points))
	}

	resp, err := http.Get(srv.URL + "/api/devices/" + d.ID + "/analytics?metric=temperature")
	if err != nil {
		t.Fatalf("get analytics: %v", err)
	}
	defer resp.Body.Close()
	var analytics struct {
		Stats map[string]models.MetricStats `json:"stats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&analytics); err != nil {
		t.Fatalf("decode analytics: %v", err)
	}
	if analytics.Stats["temperature"].Avg != 20 {
		t.Fatalf("expected avg 20, got %v", analytics.Stats["temperature"].Avg)
	}
}

func TestWebSocketReceivesEvents(t *testing.T) {
	srv := newTestServer(t)
	d := registerDevice(t, srv.URL)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()

	// Give the subscription a moment to register before ingesting.
	time.Sleep(100 * time.Millisecond)

	body := `{"metrics":{"temperature":99}}`
	resp, err := http.Post(srv.URL+"/api/devices/"+d.ID+"/telemetry", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post telemetry: %v", err)
	}
	resp.Body.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var got models.Event
	if err := conn.ReadJSON(&got); err != nil {
		t.Fatalf("read ws: %v", err)
	}
	if got.DeviceID != d.ID {
		t.Fatalf("unexpected event device %s", got.DeviceID)
	}
}
