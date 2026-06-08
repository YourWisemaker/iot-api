// Package models defines the core domain entities for the IoT platform.
package models

import "time"

// DeviceStatus represents the connectivity/health state of a device.
type DeviceStatus string

const (
	StatusOnline   DeviceStatus = "online"
	StatusOffline  DeviceStatus = "offline"
	StatusDegraded DeviceStatus = "degraded"
	StatusUnknown  DeviceStatus = "unknown"
)

// Device is a registered IoT device.
type Device struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Location     string            `json:"location"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Status       DeviceStatus      `json:"status"`
	RegisteredAt time.Time         `json:"registered_at"`
	LastSeenAt   time.Time         `json:"last_seen_at"`
}

// RegisterDeviceRequest is the payload to register a new device.
type RegisterDeviceRequest struct {
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Location string            `json:"location"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Telemetry is a single time-series measurement reported by a device.
type Telemetry struct {
	DeviceID  string             `json:"device_id"`
	Timestamp time.Time          `json:"timestamp"`
	Metrics   map[string]float64 `json:"metrics"`
}

// AlertSeverity classifies the urgency of an alert.
type AlertSeverity string

const (
	SeverityInfo     AlertSeverity = "info"
	SeverityWarning  AlertSeverity = "warning"
	SeverityCritical AlertSeverity = "critical"
)

// Alert is raised when telemetry breaches a configured rule.
type Alert struct {
	ID        string        `json:"id"`
	DeviceID  string        `json:"device_id"`
	Metric    string        `json:"metric"`
	Value     float64       `json:"value"`
	Threshold float64       `json:"threshold"`
	Severity  AlertSeverity `json:"severity"`
	Message   string        `json:"message"`
	CreatedAt time.Time     `json:"created_at"`
}

// AlertRule defines a threshold condition evaluated against telemetry.
type AlertRule struct {
	Metric    string        `json:"metric"`
	Operator  string        `json:"operator"` // ">", "<", ">=", "<=", "=="
	Threshold float64       `json:"threshold"`
	Severity  AlertSeverity `json:"severity"`
}

// MetricStats holds aggregate statistics for a single metric over a window.
type MetricStats struct {
	Metric string  `json:"metric"`
	Count  int     `json:"count"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	Avg    float64 `json:"avg"`
	Sum    float64 `json:"sum"`
	Last   float64 `json:"last"`
}

// Event is a real-time notification broadcast to subscribers (WebSocket).
type Event struct {
	Type      string      `json:"type"` // "telemetry", "status", "alert"
	DeviceID  string      `json:"device_id,omitempty"`
	Payload   interface{} `json:"payload"`
	Timestamp time.Time   `json:"timestamp"`
}
