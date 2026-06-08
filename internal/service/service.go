// Package service orchestrates the platform's domain logic: device lifecycle,
// concurrent telemetry ingestion, alert evaluation, and real-time broadcasting.
package service

import (
	"context"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/YourWisemaker/iot-api/internal/alerts"
	"github.com/YourWisemaker/iot-api/internal/models"
	"github.com/YourWisemaker/iot-api/internal/realtime"
	"github.com/YourWisemaker/iot-api/internal/store"
	"github.com/YourWisemaker/iot-api/internal/worker"
)

// Publisher broadcasts events to real-time subscribers. The local realtime.Hub
// satisfies it directly; a Redis-backed publisher fans out across instances.
type Publisher interface {
	Broadcast(event models.Event)
}

// StatusCache provides fast, write-through access to device real-time status.
// It is optional; when nil, status is served from the primary store.
type StatusCache interface {
	SetDeviceStatus(ctx context.Context, deviceID string, status models.DeviceStatus, lastSeen time.Time) error
	GetDeviceStatus(ctx context.Context, deviceID string) (models.DeviceStatus, time.Time, bool, error)
}

// Service wires together storage, the worker pool, alert engine and event hub.
type Service struct {
	store     store.Store
	pool      *worker.Pool
	alerts    *alerts.Engine
	hub       *realtime.Hub
	publisher Publisher
	cache     StatusCache

	// offlineAfter marks a device offline if not seen within this duration.
	offlineAfter time.Duration
}

// Option customizes a Service at construction time.
type Option func(*Service)

// WithPublisher overrides the event publisher (e.g. a Redis bus). When unset,
// events are broadcast directly to the local hub.
func WithPublisher(p Publisher) Option {
	return func(s *Service) {
		if p != nil {
			s.publisher = p
		}
	}
}

// WithStatusCache enables write-through caching of device status.
func WithStatusCache(c StatusCache) Option {
	return func(s *Service) { s.cache = c }
}

// New creates a Service. The worker pool must already be started (or started
// by the caller) before telemetry is ingested.
func New(s store.Store, pool *worker.Pool, eng *alerts.Engine, hub *realtime.Hub, offlineAfter time.Duration, opts ...Option) *Service {
	if offlineAfter <= 0 {
		offlineAfter = 30 * time.Second
	}
	svc := &Service{
		store:        s,
		pool:         pool,
		alerts:       eng,
		hub:          hub,
		publisher:    hub, // default: broadcast locally
		offlineAfter: offlineAfter,
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// publish emits an event to whichever publisher is configured.
func (svc *Service) publish(event models.Event) {
	svc.publisher.Broadcast(event)
}

// cacheStatus writes device status to the cache if one is configured.
func (svc *Service) cacheStatus(deviceID string, status models.DeviceStatus, lastSeen time.Time) {
	if svc.cache == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.cache.SetDeviceStatus(ctx, deviceID, status, lastSeen); err != nil {
		log.Printf("cache: set status for %s: %v", deviceID, err)
	}
}

// RegisterDevice creates and stores a new device.
func (svc *Service) RegisterDevice(req models.RegisterDeviceRequest) (models.Device, error) {
	now := time.Now().UTC()
	d := models.Device{
		ID:           uuid.NewString(),
		Name:         req.Name,
		Type:         req.Type,
		Location:     req.Location,
		Metadata:     req.Metadata,
		Status:       models.StatusUnknown,
		RegisteredAt: now,
		LastSeenAt:   time.Time{},
	}
	if err := svc.store.CreateDevice(d); err != nil {
		return models.Device{}, err
	}
	svc.cacheStatus(d.ID, d.Status, time.Time{})
	svc.publish(models.Event{
		Type:      "device",
		DeviceID:  d.ID,
		Payload:   d,
		Timestamp: now,
	})
	return d, nil
}

// GetDevice returns a single device. When a status cache is configured, the
// freshest cached status is overlaid on the stored record.
func (svc *Service) GetDevice(id string) (models.Device, error) {
	d, err := svc.store.GetDevice(id)
	if err != nil {
		return d, err
	}
	if svc.cache != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if status, seen, ok, cerr := svc.cache.GetDeviceStatus(ctx, id); cerr == nil && ok {
			d.Status = status
			if !seen.IsZero() {
				d.LastSeenAt = seen
			}
		}
	}
	return d, nil
}

// ListDevices returns all devices.
func (svc *Service) ListDevices() []models.Device {
	return svc.store.ListDevices()
}

// DeleteDevice removes a device.
func (svc *Service) DeleteDevice(id string) error {
	return svc.store.DeleteDevice(id)
}

// IngestTelemetry submits a telemetry sample for asynchronous processing via
// the worker pool. It returns false if the pool rejected the job (backpressure).
func (svc *Service) IngestTelemetry(t models.Telemetry) bool {
	if t.Timestamp.IsZero() {
		t.Timestamp = time.Now().UTC()
	}
	return svc.pool.Submit(func(ctx context.Context) {
		svc.processTelemetry(t)
	})
}

// processTelemetry runs on a worker goroutine: persist, mark online, evaluate
// alerts and broadcast real-time events.
func (svc *Service) processTelemetry(t models.Telemetry) {
	if err := svc.store.AddTelemetry(t); err != nil {
		log.Printf("ingest: store telemetry for %s: %v", t.DeviceID, err)
		return
	}

	if err := svc.store.UpdateDeviceStatus(t.DeviceID, models.StatusOnline, t.Timestamp); err != nil {
		log.Printf("ingest: update status for %s: %v", t.DeviceID, err)
	}
	svc.cacheStatus(t.DeviceID, models.StatusOnline, t.Timestamp)

	svc.publish(models.Event{
		Type:      "telemetry",
		DeviceID:  t.DeviceID,
		Payload:   t,
		Timestamp: t.Timestamp,
	})

	for _, alert := range svc.alerts.Evaluate(t) {
		if err := svc.store.AddAlert(alert); err != nil {
			log.Printf("ingest: store alert for %s: %v", t.DeviceID, err)
			continue
		}
		svc.publish(models.Event{
			Type:      "alert",
			DeviceID:  alert.DeviceID,
			Payload:   alert,
			Timestamp: alert.CreatedAt,
		})
	}
}

// GetTelemetry returns telemetry history for a device.
func (svc *Service) GetTelemetry(deviceID string, since time.Time, limit int) []models.Telemetry {
	return svc.store.GetTelemetry(deviceID, since, limit)
}

// GetTelemetrySince returns the full telemetry window for analytics.
func (svc *Service) GetTelemetrySince(deviceID string, since time.Time) []models.Telemetry {
	return svc.store.GetTelemetry(deviceID, since, 0)
}

// ListAlerts returns alerts, optionally filtered by device.
func (svc *Service) ListAlerts(deviceID string) []models.Alert {
	return svc.store.ListAlerts(deviceID)
}

// Subscribe registers a real-time event subscriber.
func (svc *Service) Subscribe() chan models.Event {
	return svc.hub.Subscribe()
}

// Unsubscribe removes a real-time event subscriber.
func (svc *Service) Unsubscribe(ch chan models.Event) {
	svc.hub.Unsubscribe(ch)
}

// ReconcileStatuses marks devices offline if they have not reported within the
// offline window. Intended to be run periodically.
func (svc *Service) ReconcileStatuses() {
	now := time.Now().UTC()
	for _, d := range svc.store.ListDevices() {
		if d.LastSeenAt.IsZero() {
			continue
		}
		if now.Sub(d.LastSeenAt) > svc.offlineAfter && d.Status != models.StatusOffline {
			if err := svc.store.UpdateDeviceStatus(d.ID, models.StatusOffline, time.Time{}); err != nil {
				continue
			}
			svc.cacheStatus(d.ID, models.StatusOffline, time.Time{})
			svc.publish(models.Event{
				Type:      "status",
				DeviceID:  d.ID,
				Payload:   map[string]string{"status": string(models.StatusOffline)},
				Timestamp: now,
			})
		}
	}
}

// RunStatusReconciler periodically reconciles device statuses until ctx is done.
func (svc *Service) RunStatusReconciler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			svc.ReconcileStatuses()
		}
	}
}
