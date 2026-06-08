// Package store provides thread-safe persistence for platform entities.
// The default implementation is in-memory and safe for concurrent use.
package store

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// ErrNotFound is returned when a requested entity does not exist.
var ErrNotFound = errors.New("not found")

// Store is the persistence contract for the platform.
type Store interface {
	CreateDevice(d models.Device) error
	GetDevice(id string) (models.Device, error)
	ListDevices() []models.Device
	UpdateDevice(id string, name, deviceType, location string, metadata map[string]string) error
	UpdateDeviceStatus(id string, status models.DeviceStatus, seen time.Time) error
	DeleteDevice(id string) error

	AddTelemetry(t models.Telemetry) error
	GetTelemetry(deviceID string, since time.Time, limit int) []models.Telemetry

	AddAlert(a models.Alert) error
	ListAlerts(deviceID string) []models.Alert
}

// MemoryStore is a concurrency-safe, in-memory Store implementation.
type MemoryStore struct {
	mu        sync.RWMutex
	devices   map[string]models.Device
	telemetry map[string][]models.Telemetry
	alerts    map[string][]models.Alert
	users     map[string]models.User         // keyed by user ID
	refresh   map[string]models.RefreshToken // keyed by token hash
	// maxHistory caps the number of telemetry points retained per device.
	maxHistory int
}

// NewMemoryStore creates an empty in-memory store. maxHistory <= 0 means unbounded.
func NewMemoryStore(maxHistory int) *MemoryStore {
	return &MemoryStore{
		devices:    make(map[string]models.Device),
		telemetry:  make(map[string][]models.Telemetry),
		alerts:     make(map[string][]models.Alert),
		users:      make(map[string]models.User),
		refresh:    make(map[string]models.RefreshToken),
		maxHistory: maxHistory,
	}
}

// CreateDevice stores a new device. It errors if the ID already exists.
func (s *MemoryStore) CreateDevice(d models.Device) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[d.ID]; ok {
		return errors.New("device already exists")
	}
	s.devices[d.ID] = d
	return nil
}

// GetDevice returns a device by ID.
func (s *MemoryStore) GetDevice(id string) (models.Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.devices[id]
	if !ok {
		return models.Device{}, ErrNotFound
	}
	return d, nil
}

// ListDevices returns all devices sorted by registration time.
func (s *MemoryStore) ListDevices() []models.Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]models.Device, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].RegisteredAt.Before(out[j].RegisteredAt)
	})
	return out
}

// UpdateDevice replaces a device's mutable descriptive fields, preserving
// status, registration and last-seen timestamps.
func (s *MemoryStore) UpdateDevice(id string, name, deviceType, location string, metadata map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[id]
	if !ok {
		return ErrNotFound
	}
	d.Name = name
	d.Type = deviceType
	d.Location = location
	d.Metadata = metadata
	s.devices[id] = d
	return nil
}

// UpdateDeviceStatus updates a device's status and last-seen timestamp.
func (s *MemoryStore) UpdateDeviceStatus(id string, status models.DeviceStatus, seen time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[id]
	if !ok {
		return ErrNotFound
	}
	d.Status = status
	if !seen.IsZero() {
		d.LastSeenAt = seen
	}
	s.devices[id] = d
	return nil
}

// DeleteDevice removes a device and its associated data.
func (s *MemoryStore) DeleteDevice(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[id]; !ok {
		return ErrNotFound
	}
	delete(s.devices, id)
	delete(s.telemetry, id)
	delete(s.alerts, id)
	return nil
}

// AddTelemetry appends a telemetry point, enforcing the history cap.
func (s *MemoryStore) AddTelemetry(t models.Telemetry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[t.DeviceID]; !ok {
		return ErrNotFound
	}
	series := append(s.telemetry[t.DeviceID], t)
	if s.maxHistory > 0 && len(series) > s.maxHistory {
		series = series[len(series)-s.maxHistory:]
	}
	s.telemetry[t.DeviceID] = series
	return nil
}

// GetTelemetry returns telemetry for a device since the given time, newest last.
// A limit <= 0 means no limit.
func (s *MemoryStore) GetTelemetry(deviceID string, since time.Time, limit int) []models.Telemetry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	series := s.telemetry[deviceID]
	out := make([]models.Telemetry, 0, len(series))
	for _, t := range series {
		if since.IsZero() || t.Timestamp.After(since) || t.Timestamp.Equal(since) {
			out = append(out, t)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// AddAlert records an alert for a device.
func (s *MemoryStore) AddAlert(a models.Alert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.alerts[a.DeviceID] = append(s.alerts[a.DeviceID], a)
	return nil
}

// ListAlerts returns alerts for a device, or all alerts when deviceID is empty.
func (s *MemoryStore) ListAlerts(deviceID string) []models.Alert {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if deviceID != "" {
		out := make([]models.Alert, len(s.alerts[deviceID]))
		copy(out, s.alerts[deviceID])
		return out
	}
	var out []models.Alert
	for _, list := range s.alerts {
		out = append(out, list...)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}
