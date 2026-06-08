// Package mqtt integrates the platform with an MQTT broker for telemetry
// ingestion. Devices publish telemetry to "devices/<id>/telemetry".
package mqtt

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	mqttlib "github.com/eclipse/paho.mqtt.golang"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// Ingestor is the subset of the platform service the MQTT bridge needs.
type Ingestor interface {
	IngestTelemetry(t models.Telemetry) bool
}

// Config configures the MQTT bridge.
type Config struct {
	BrokerURL    string        // e.g. "tcp://localhost:1883"
	ClientID     string        // MQTT client identifier
	TopicPrefix  string        // default "devices"
	Username     string        // optional
	Password     string        // optional
	ConnectRetry time.Duration // retry interval for reconnects
}

// Bridge subscribes to device telemetry topics and forwards messages to the
// platform's ingestion pipeline.
type Bridge struct {
	client  mqttlib.Client
	cfg     Config
	ingest  Ingestor
	prefix  string
}

// NewBridge constructs an MQTT bridge. It does not connect until Connect is called.
func NewBridge(cfg Config, ingest Ingestor) *Bridge {
	prefix := cfg.TopicPrefix
	if prefix == "" {
		prefix = "devices"
	}
	if cfg.ClientID == "" {
		cfg.ClientID = "iot-api-" + fmt.Sprint(time.Now().UnixNano())
	}
	if cfg.ConnectRetry <= 0 {
		cfg.ConnectRetry = 5 * time.Second
	}

	opts := mqttlib.NewClientOptions().
		AddBroker(cfg.BrokerURL).
		SetClientID(cfg.ClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(cfg.ConnectRetry)

	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
		opts.SetPassword(cfg.Password)
	}

	b := &Bridge{cfg: cfg, ingest: ingest, prefix: prefix}

	opts.SetOnConnectHandler(func(c mqttlib.Client) {
		topic := prefix + "/+/telemetry"
		if tok := c.Subscribe(topic, 1, b.onMessage); tok.Wait() && tok.Error() != nil {
			log.Printf("mqtt: subscribe %s: %v", topic, tok.Error())
			return
		}
		log.Printf("mqtt: subscribed to %s", topic)
	})

	b.client = mqttlib.NewClient(opts)
	return b
}

// Connect establishes the broker connection.
func (b *Bridge) Connect() error {
	tok := b.client.Connect()
	tok.Wait()
	return tok.Error()
}

// Disconnect gracefully closes the broker connection.
func (b *Bridge) Disconnect() {
	if b.client != nil && b.client.IsConnected() {
		b.client.Disconnect(250)
	}
}

// onMessage decodes an incoming telemetry message and ingests it.
func (b *Bridge) onMessage(_ mqttlib.Client, msg mqttlib.Message) {
	deviceID := deviceIDFromTopic(msg.Topic(), b.prefix)
	if deviceID == "" {
		return
	}

	var payload struct {
		Timestamp *time.Time         `json:"timestamp"`
		Metrics   map[string]float64 `json:"metrics"`
	}
	if err := json.Unmarshal(msg.Payload(), &payload); err != nil {
		log.Printf("mqtt: decode payload for %s: %v", deviceID, err)
		return
	}

	t := models.Telemetry{
		DeviceID: deviceID,
		Metrics:  payload.Metrics,
	}
	if payload.Timestamp != nil {
		t.Timestamp = *payload.Timestamp
	} else {
		t.Timestamp = time.Now().UTC()
	}

	b.ingest.IngestTelemetry(t)
}

// deviceIDFromTopic extracts the device id from "<prefix>/<id>/telemetry".
func deviceIDFromTopic(topic, prefix string) string {
	parts := strings.Split(topic, "/")
	if len(parts) != 3 || parts[0] != prefix || parts[2] != "telemetry" {
		return ""
	}
	return parts[1]
}
