// Package api exposes the platform over HTTP and WebSocket.
package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"github.com/YourWisemaker/iot-api/internal/analytics"
	"github.com/YourWisemaker/iot-api/internal/auth"
	"github.com/YourWisemaker/iot-api/internal/models"
	"github.com/YourWisemaker/iot-api/internal/service"
	"github.com/YourWisemaker/iot-api/internal/store"
)

// Handler holds dependencies for the HTTP API.
type Handler struct {
	svc      *service.Service
	auth     *auth.Manager // nil disables authentication
	upgrader websocket.Upgrader
}

// NewHandler creates an API handler backed by the given service. A nil auth
// manager disables authentication (suitable for local development).
func NewHandler(svc *service.Service, authManager *auth.Manager) *Handler {
	return &Handler{
		svc:  svc,
		auth: authManager,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			// Allow all origins; tighten for production deployments.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// Routes builds the router with all platform endpoints.
func (h *Handler) Routes() *mux.Router {
	r := mux.NewRouter()
	r.Use(jsonContentType)
	if h.auth != nil {
		r.Use(h.auth.Middleware)
	}

	r.HandleFunc("/health", h.health).Methods(http.MethodGet)
	r.HandleFunc("/api/auth/login", h.login).Methods(http.MethodPost)

	r.HandleFunc("/api/devices", h.registerDevice).Methods(http.MethodPost)
	r.HandleFunc("/api/devices", h.listDevices).Methods(http.MethodGet)
	r.HandleFunc("/api/devices/{id}", h.getDevice).Methods(http.MethodGet)
	r.HandleFunc("/api/devices/{id}", h.deleteDevice).Methods(http.MethodDelete)

	r.HandleFunc("/api/devices/{id}/telemetry", h.ingestTelemetry).Methods(http.MethodPost)
	r.HandleFunc("/api/devices/{id}/telemetry", h.getTelemetry).Methods(http.MethodGet)
	r.HandleFunc("/api/devices/{id}/analytics", h.getAnalytics).Methods(http.MethodGet)

	r.HandleFunc("/api/alerts", h.listAlerts).Methods(http.MethodGet)

	// WebSocket endpoint for real-time events.
	r.HandleFunc("/ws", h.websocket)

	return r
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// login validates credentials and returns a signed JWT.
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeError(w, http.StatusNotFound, "authentication is disabled")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !h.auth.ValidateCredentials(req.Username, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	token, err := h.auth.Generate(req.Username, []string{"admin"})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":      token,
		"token_type": "Bearer",
		"expires_in": int(h.auth.TTL().Seconds()),
	})
}

func (h *Handler) registerDevice(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterDeviceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	device, err := h.svc.RegisterDevice(req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, device)
}

func (h *Handler) listDevices(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.ListDevices())
}

func (h *Handler) getDevice(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	device, err := h.svc.GetDevice(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, device)
}

func (h *Handler) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if err := h.svc.DeleteDevice(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "device not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ingestTelemetry(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	if _, err := h.svc.GetDevice(id); err != nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}

	var body struct {
		Timestamp *time.Time         `json:"timestamp"`
		Metrics   map[string]float64 `json:"metrics"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(body.Metrics) == 0 {
		writeError(w, http.StatusBadRequest, "metrics are required")
		return
	}

	t := models.Telemetry{DeviceID: id, Metrics: body.Metrics}
	if body.Timestamp != nil {
		t.Timestamp = *body.Timestamp
	}

	if ok := h.svc.IngestTelemetry(t); !ok {
		writeError(w, http.StatusServiceUnavailable, "ingestion queue unavailable")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
}

func (h *Handler) getTelemetry(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	since := parseSince(r)
	limit := parseLimit(r)
	writeJSON(w, http.StatusOK, h.svc.GetTelemetry(id, since, limit))
}

func (h *Handler) getAnalytics(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	since := parseSince(r)
	points := h.svc.GetTelemetrySince(id, since)

	resp := map[string]interface{}{
		"device_id": id,
		"window":    points,
		"stats":     analytics.Aggregate(points),
	}
	if metric := r.URL.Query().Get("metric"); metric != "" {
		resp["series"] = analytics.TimeSeries(points, metric)
	}
	// Avoid echoing the raw window unless asked; keep payload focused.
	delete(resp, "window")
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listAlerts(w http.ResponseWriter, r *http.Request) {
	deviceID := r.URL.Query().Get("device_id")
	writeJSON(w, http.StatusOK, h.svc.ListAlerts(deviceID))
}

// websocket upgrades the connection and streams real-time events.
func (h *Handler) websocket(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	events := h.svc.Subscribe()
	defer h.svc.Unsubscribe(events)

	// Reader goroutine: detect client disconnect.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-closed:
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if err := conn.WriteJSON(ev); err != nil {
				return
			}
		}
	}
}

// --- helpers ---

func parseSince(r *http.Request) time.Time {
	raw := r.URL.Query().Get("since")
	if raw == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t
	}
	// Fall back to a relative duration like "1h".
	if d, err := time.ParseDuration(raw); err == nil {
		return time.Now().UTC().Add(-d)
	}
	return time.Time{}
}

func parseLimit(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func jsonContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
