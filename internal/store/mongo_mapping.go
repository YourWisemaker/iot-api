package store

import (
	"time"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// deviceDoc is the BSON representation of a device.
type deviceDoc struct {
	ID           string            `bson:"id"`
	Name         string            `bson:"name"`
	Type         string            `bson:"type"`
	Location     string            `bson:"location"`
	Metadata     map[string]string `bson:"metadata,omitempty"`
	Status       string            `bson:"status"`
	RegisteredAt time.Time         `bson:"registered_at"`
	LastSeenAt   time.Time         `bson:"last_seen_at"`
}

func deviceToDoc(d models.Device) deviceDoc {
	return deviceDoc{
		ID:           d.ID,
		Name:         d.Name,
		Type:         d.Type,
		Location:     d.Location,
		Metadata:     d.Metadata,
		Status:       string(d.Status),
		RegisteredAt: d.RegisteredAt,
		LastSeenAt:   d.LastSeenAt,
	}
}

func docToDevice(doc deviceDoc) models.Device {
	return models.Device{
		ID:           doc.ID,
		Name:         doc.Name,
		Type:         doc.Type,
		Location:     doc.Location,
		Metadata:     doc.Metadata,
		Status:       models.DeviceStatus(doc.Status),
		RegisteredAt: doc.RegisteredAt,
		LastSeenAt:   doc.LastSeenAt,
	}
}

// alertDoc is the BSON representation of an alert.
type alertDoc struct {
	ID        string    `bson:"id"`
	DeviceID  string    `bson:"device_id"`
	Metric    string    `bson:"metric"`
	Value     float64   `bson:"value"`
	Threshold float64   `bson:"threshold"`
	Severity  string    `bson:"severity"`
	Message   string    `bson:"message"`
	CreatedAt time.Time `bson:"created_at"`
}

func alertToDoc(a models.Alert) alertDoc {
	return alertDoc{
		ID:        a.ID,
		DeviceID:  a.DeviceID,
		Metric:    a.Metric,
		Value:     a.Value,
		Threshold: a.Threshold,
		Severity:  string(a.Severity),
		Message:   a.Message,
		CreatedAt: a.CreatedAt,
	}
}

func docToAlert(doc alertDoc) models.Alert {
	return models.Alert{
		ID:        doc.ID,
		DeviceID:  doc.DeviceID,
		Metric:    doc.Metric,
		Value:     doc.Value,
		Threshold: doc.Threshold,
		Severity:  models.AlertSeverity(doc.Severity),
		Message:   doc.Message,
		CreatedAt: doc.CreatedAt,
	}
}
