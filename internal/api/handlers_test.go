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
	"github.com/YourWisemaker/iot-api/internal/auth"
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
	srv := httptest.NewServer(NewHandler(svc, nil, nil).Routes())
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

func newAuthedTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	pool := worker.NewPool(4, 64)
	pool.Start()
	t.Cleanup(pool.Stop)

	engine := alerts.NewEngine()
	st := store.NewMemoryStore(100)
	svc := service.New(st, pool, engine, realtime.NewHub(16), time.Second)

	mgr := auth.NewManager(auth.Config{Secret: "test-secret", TTL: time.Hour})
	authSvc := auth.NewService(st, mgr, time.Hour)
	if _, err := authSvc.CreateUser("admin", "supersecret", []string{models.RoleAdmin}); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if _, err := authSvc.CreateUser("viewer", "supersecret", []string{models.RoleViewer}); err != nil {
		t.Fatalf("seed viewer: %v", err)
	}

	srv := httptest.NewServer(NewHandler(svc, mgr, authSvc).Routes())
	t.Cleanup(srv.Close)
	return srv
}

// login authenticates and returns the access token, refresh token and status.
func login(t *testing.T, base, user, pass string) (string, string, int) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := http.Post(base+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.AccessToken, out.RefreshToken, resp.StatusCode
}

func TestProtectedRouteRequiresToken(t *testing.T) {
	srv := newAuthedTestServer(t)
	resp, err := http.Get(srv.URL + "/api/devices")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}
}

func TestLoginRejectsBadCredentials(t *testing.T) {
	srv := newAuthedTestServer(t)
	if _, _, code := login(t, srv.URL, "admin", "wrong"); code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for bad credentials, got %d", code)
	}
}

func TestLoginAndAccessProtectedRoute(t *testing.T) {
	srv := newAuthedTestServer(t)
	token, _, code := login(t, srv.URL, "admin", "supersecret")
	if code != http.StatusOK || token == "" {
		t.Fatalf("login failed: code=%d token=%q", code, token)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get devices: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", resp.StatusCode)
	}
}

func TestWebSocketRequiresToken(t *testing.T) {
	srv := newAuthedTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	// No token: handshake should be rejected.
	if _, _, err := websocket.DefaultDialer.Dial(wsURL, nil); err == nil {
		t.Fatal("expected WebSocket handshake to fail without token")
	}

	// With a token in the query string: handshake should succeed.
	token, _, _ := login(t, srv.URL, "admin", "supersecret")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL+"?token="+token, nil)
	if err != nil {
		t.Fatalf("expected WebSocket to connect with token: %v", err)
	}
	conn.Close()
}

func authedReq(t *testing.T, method, url, token string, body string) *http.Response {
	t.Helper()
	var r *http.Request
	if body != "" {
		r, _ = http.NewRequest(method, url, strings.NewReader(body))
	} else {
		r, _ = http.NewRequest(method, url, nil)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func TestViewerCannotRegisterDevice(t *testing.T) {
	srv := newAuthedTestServer(t)
	token, _, _ := login(t, srv.URL, "viewer", "supersecret")

	resp := authedReq(t, http.MethodPost, srv.URL+"/api/devices", token, `{"name":"d"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer registering device, got %d", resp.StatusCode)
	}
}

func TestViewerCanReadDevices(t *testing.T) {
	srv := newAuthedTestServer(t)
	token, _, _ := login(t, srv.URL, "viewer", "supersecret")

	resp := authedReq(t, http.MethodGet, srv.URL+"/api/devices", token, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected viewer to read devices, got %d", resp.StatusCode)
	}
}

func TestAdminCanRegisterDevice(t *testing.T) {
	srv := newAuthedTestServer(t)
	token, _, _ := login(t, srv.URL, "admin", "supersecret")

	resp := authedReq(t, http.MethodPost, srv.URL+"/api/devices", token, `{"name":"d"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected admin to register device, got %d", resp.StatusCode)
	}
}

func TestAdminCanCreateUser(t *testing.T) {
	srv := newAuthedTestServer(t)
	token, _, _ := login(t, srv.URL, "admin", "supersecret")

	resp := authedReq(t, http.MethodPost, srv.URL+"/api/users", token,
		`{"username":"dave","password":"supersecret","roles":["viewer"]}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating user, got %d", resp.StatusCode)
	}

	// The new user can log in.
	if _, _, code := login(t, srv.URL, "dave", "supersecret"); code != http.StatusOK {
		t.Fatalf("new user could not log in, got %d", code)
	}
}

func TestViewerCannotListUsers(t *testing.T) {
	srv := newAuthedTestServer(t)
	token, _, _ := login(t, srv.URL, "viewer", "supersecret")

	resp := authedReq(t, http.MethodGet, srv.URL+"/api/users", token, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer listing users, got %d", resp.StatusCode)
	}
}

func TestRefreshAndLogoutFlow(t *testing.T) {
	srv := newAuthedTestServer(t)
	_, refresh, _ := login(t, srv.URL, "admin", "supersecret")

	// Refresh issues a new pair.
	resp, err := http.Post(srv.URL+"/api/auth/refresh", "application/json",
		strings.NewReader(`{"refresh_token":"`+refresh+`"}`))
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	var pair struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&pair)
	resp.Body.Close()
	if pair.AccessToken == "" || pair.RefreshToken == "" {
		t.Fatal("expected new token pair from refresh")
	}

	// Old refresh token is now invalid (rotation).
	resp2, _ := http.Post(srv.URL+"/api/auth/refresh", "application/json",
		strings.NewReader(`{"refresh_token":"`+refresh+`"}`))
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 reusing rotated token, got %d", resp2.StatusCode)
	}
	resp2.Body.Close()

	// Logout revokes the current refresh token.
	resp3, _ := http.Post(srv.URL+"/api/auth/logout", "application/json",
		strings.NewReader(`{"refresh_token":"`+pair.RefreshToken+`"}`))
	resp3.Body.Close()
	resp4, _ := http.Post(srv.URL+"/api/auth/refresh", "application/json",
		strings.NewReader(`{"refresh_token":"`+pair.RefreshToken+`"}`))
	if resp4.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", resp4.StatusCode)
	}
	resp4.Body.Close()
}

func TestAdminCanUpdateDevice(t *testing.T) {
	srv := newAuthedTestServer(t)
	token, _, _ := login(t, srv.URL, "admin", "supersecret")

	resp := authedReq(t, http.MethodPost, srv.URL+"/api/devices", token, `{"name":"old","type":"sensor"}`)
	var d models.Device
	_ = json.NewDecoder(resp.Body).Decode(&d)
	resp.Body.Close()

	upd := authedReq(t, http.MethodPut, srv.URL+"/api/devices/"+d.ID, token,
		`{"name":"new","type":"gateway","location":"lab"}`)
	defer upd.Body.Close()
	if upd.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating device, got %d", upd.StatusCode)
	}
	var got models.Device
	_ = json.NewDecoder(upd.Body).Decode(&got)
	if got.Name != "new" || got.Type != "gateway" {
		t.Fatalf("device not updated: %+v", got)
	}
}

func TestViewerCannotUpdateDevice(t *testing.T) {
	srv := newAuthedTestServer(t)
	adminToken, _, _ := login(t, srv.URL, "admin", "supersecret")
	resp := authedReq(t, http.MethodPost, srv.URL+"/api/devices", adminToken, `{"name":"d"}`)
	var d models.Device
	_ = json.NewDecoder(resp.Body).Decode(&d)
	resp.Body.Close()

	viewerToken, _, _ := login(t, srv.URL, "viewer", "supersecret")
	upd := authedReq(t, http.MethodPut, srv.URL+"/api/devices/"+d.ID, viewerToken, `{"name":"x"}`)
	defer upd.Body.Close()
	if upd.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for viewer update, got %d", upd.StatusCode)
	}
}

func TestUpdateMissingDeviceReturns404(t *testing.T) {
	srv := newAuthedTestServer(t)
	token, _, _ := login(t, srv.URL, "admin", "supersecret")
	upd := authedReq(t, http.MethodPut, srv.URL+"/api/devices/ghost", token, `{"name":"x"}`)
	defer upd.Body.Close()
	if upd.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", upd.StatusCode)
	}
}

func TestAdminGetAndUpdateUser(t *testing.T) {
	srv := newAuthedTestServer(t)
	token, _, _ := login(t, srv.URL, "admin", "supersecret")

	// Create a user to operate on.
	resp := authedReq(t, http.MethodPost, srv.URL+"/api/users", token,
		`{"username":"erin","password":"supersecret","roles":["viewer"]}`)
	var created models.User
	_ = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()

	// Get by ID.
	get := authedReq(t, http.MethodGet, srv.URL+"/api/users/"+created.ID, token, "")
	if get.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 getting user, got %d", get.StatusCode)
	}
	get.Body.Close()

	// Update roles.
	upd := authedReq(t, http.MethodPut, srv.URL+"/api/users/"+created.ID, token, `{"roles":["admin"]}`)
	defer upd.Body.Close()
	if upd.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 updating user, got %d", upd.StatusCode)
	}
	var updated models.User
	_ = json.NewDecoder(upd.Body).Decode(&updated)
	if len(updated.Roles) != 1 || updated.Roles[0] != "admin" {
		t.Fatalf("roles not updated: %v", updated.Roles)
	}
}
