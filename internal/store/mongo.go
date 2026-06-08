// Package store: MongoDB (NoSQL) implementation of the Store interface.
package store

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// MongoStore persists platform entities in MongoDB. It satisfies Store.
type MongoStore struct {
	client     *mongo.Client
	devices    *mongo.Collection
	telemetry  *mongo.Collection
	alerts     *mongo.Collection
	maxHistory int
	timeout    time.Duration
}

// MongoConfig configures a MongoStore connection.
type MongoConfig struct {
	URI        string
	Database   string
	MaxHistory int
	Timeout    time.Duration
}

// NewMongoStore connects to MongoDB, verifies the connection and ensures
// indexes. The caller owns the returned store and must call Close.
func NewMongoStore(ctx context.Context, cfg MongoConfig) (*MongoStore, error) {
	if cfg.Database == "" {
		cfg.Database = "iot"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}

	connectCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	client, err := mongo.Connect(connectCtx, options.Client().ApplyURI(cfg.URI))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(connectCtx, nil); err != nil {
		return nil, err
	}

	db := client.Database(cfg.Database)
	s := &MongoStore{
		client:     client,
		devices:    db.Collection("devices"),
		telemetry:  db.Collection("telemetry"),
		alerts:     db.Collection("alerts"),
		maxHistory: cfg.MaxHistory,
		timeout:    cfg.Timeout,
	}
	if err := s.ensureIndexes(connectCtx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *MongoStore) ensureIndexes(ctx context.Context) error {
	_, err := s.devices.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	if err != nil {
		return err
	}
	_, err = s.telemetry.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "device_id", Value: 1}, {Key: "timestamp", Value: 1}}},
	})
	if err != nil {
		return err
	}
	_, err = s.alerts.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{{Key: "device_id", Value: 1}, {Key: "created_at", Value: 1}},
	})
	return err
}

// Close disconnects the underlying client.
func (s *MongoStore) Close(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

func (s *MongoStore) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.timeout)
}

// telemetryDoc is the BSON representation of a telemetry point. Maps cannot use
// float64 values portably across drivers, so metrics are stored as documents.
type telemetryDoc struct {
	DeviceID  string             `bson:"device_id"`
	Timestamp time.Time          `bson:"timestamp"`
	Metrics   map[string]float64 `bson:"metrics"`
}

// CreateDevice inserts a new device.
func (s *MongoStore) CreateDevice(d models.Device) error {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.devices.InsertOne(ctx, deviceToDoc(d))
	if mongo.IsDuplicateKeyError(err) {
		return errors.New("device already exists")
	}
	return err
}

// GetDevice fetches a device by ID.
func (s *MongoStore) GetDevice(id string) (models.Device, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	var doc deviceDoc
	err := s.devices.FindOne(ctx, bson.M{"id": id}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.Device{}, ErrNotFound
	}
	if err != nil {
		return models.Device{}, err
	}
	return docToDevice(doc), nil
}

// ListDevices returns all devices sorted by registration time.
func (s *MongoStore) ListDevices() []models.Device {
	ctx, cancel := s.ctx()
	defer cancel()
	cur, err := s.devices.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "registered_at", Value: 1}}))
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)

	var out []models.Device
	for cur.Next(ctx) {
		var doc deviceDoc
		if err := cur.Decode(&doc); err != nil {
			continue
		}
		out = append(out, docToDevice(doc))
	}
	return out
}

// UpdateDeviceStatus updates a device's status and optionally last-seen time.
func (s *MongoStore) UpdateDeviceStatus(id string, status models.DeviceStatus, seen time.Time) error {
	ctx, cancel := s.ctx()
	defer cancel()
	set := bson.M{"status": string(status)}
	if !seen.IsZero() {
		set["last_seen_at"] = seen
	}
	res, err := s.devices.UpdateOne(ctx, bson.M{"id": id}, bson.M{"$set": set})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteDevice removes a device and its associated data.
func (s *MongoStore) DeleteDevice(id string) error {
	ctx, cancel := s.ctx()
	defer cancel()
	res, err := s.devices.DeleteOne(ctx, bson.M{"id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	_, _ = s.telemetry.DeleteMany(ctx, bson.M{"device_id": id})
	_, _ = s.alerts.DeleteMany(ctx, bson.M{"device_id": id})
	return nil
}

// AddTelemetry appends a telemetry point and trims history beyond maxHistory.
func (s *MongoStore) AddTelemetry(t models.Telemetry) error {
	ctx, cancel := s.ctx()
	defer cancel()

	if err := s.devices.FindOne(ctx, bson.M{"id": t.DeviceID}).Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return ErrNotFound
		}
		return err
	}

	_, err := s.telemetry.InsertOne(ctx, telemetryDoc{
		DeviceID:  t.DeviceID,
		Timestamp: t.Timestamp,
		Metrics:   t.Metrics,
	})
	if err != nil {
		return err
	}
	s.trimHistory(ctx, t.DeviceID)
	return nil
}

// trimHistory removes the oldest telemetry beyond the configured cap.
func (s *MongoStore) trimHistory(ctx context.Context, deviceID string) {
	if s.maxHistory <= 0 {
		return
	}
	count, err := s.telemetry.CountDocuments(ctx, bson.M{"device_id": deviceID})
	if err != nil || count <= int64(s.maxHistory) {
		return
	}
	excess := count - int64(s.maxHistory)
	cur, err := s.telemetry.Find(ctx, bson.M{"device_id": deviceID},
		options.Find().SetSort(bson.D{{Key: "timestamp", Value: 1}}).SetLimit(excess))
	if err != nil {
		return
	}
	defer cur.Close(ctx)
	var ids []interface{}
	for cur.Next(ctx) {
		var doc bson.M
		if err := cur.Decode(&doc); err == nil {
			ids = append(ids, doc["_id"])
		}
	}
	if len(ids) > 0 {
		_, _ = s.telemetry.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
	}
}

// GetTelemetry returns telemetry for a device since the given time, newest last.
func (s *MongoStore) GetTelemetry(deviceID string, since time.Time, limit int) []models.Telemetry {
	ctx, cancel := s.ctx()
	defer cancel()

	filter := bson.M{"device_id": deviceID}
	if !since.IsZero() {
		filter["timestamp"] = bson.M{"$gte": since}
	}
	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: 1}})
	if limit > 0 {
		// Fetch the newest `limit` points, then re-sort ascending below.
		opts = options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}).SetLimit(int64(limit))
	}

	cur, err := s.telemetry.Find(ctx, filter, opts)
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)

	var out []models.Telemetry
	for cur.Next(ctx) {
		var doc telemetryDoc
		if err := cur.Decode(&doc); err != nil {
			continue
		}
		out = append(out, models.Telemetry{
			DeviceID:  doc.DeviceID,
			Timestamp: doc.Timestamp,
			Metrics:   doc.Metrics,
		})
	}
	if limit > 0 {
		// Results came newest-first; reverse to ascending.
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

// AddAlert records an alert.
func (s *MongoStore) AddAlert(a models.Alert) error {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.alerts.InsertOne(ctx, alertToDoc(a))
	return err
}

// ListAlerts returns alerts for a device, or all alerts when deviceID is empty.
func (s *MongoStore) ListAlerts(deviceID string) []models.Alert {
	ctx, cancel := s.ctx()
	defer cancel()
	filter := bson.M{}
	if deviceID != "" {
		filter["device_id"] = deviceID
	}
	cur, err := s.alerts.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)

	var out []models.Alert
	for cur.Next(ctx) {
		var doc alertDoc
		if err := cur.Decode(&doc); err != nil {
			continue
		}
		out = append(out, docToAlert(doc))
	}
	return out
}
