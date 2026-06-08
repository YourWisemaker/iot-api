// Package cache provides a Redis-backed device status cache and a distributed
// event bus used to fan real-time events out across multiple API instances.
package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/YourWisemaker/iot-api/internal/models"
)

const (
	statusKeyPrefix = "device:status:"
	eventsChannel   = "iot:events"
)

// Redis wraps a go-redis client to provide status caching and pub/sub.
type Redis struct {
	client *redis.Client
	ttl    time.Duration
}

// Config configures the Redis connection.
type Config struct {
	Addr     string        // host:port
	Password string        // optional
	DB       int           // logical database
	StatusTTL time.Duration // expiry for cached status entries
}

// New connects to Redis and verifies the connection with a ping.
func New(ctx context.Context, cfg Config) (*Redis, error) {
	if cfg.StatusTTL <= 0 {
		cfg.StatusTTL = 5 * time.Minute
	}
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}
	return &Redis{client: client, ttl: cfg.StatusTTL}, nil
}

// Close releases the underlying client.
func (r *Redis) Close() error { return r.client.Close() }

// statusEntry is the cached representation of a device's real-time status.
type statusEntry struct {
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
}

// SetDeviceStatus writes a device's status to the cache with the configured TTL.
func (r *Redis) SetDeviceStatus(ctx context.Context, deviceID string, status models.DeviceStatus, lastSeen time.Time) error {
	payload, err := json.Marshal(statusEntry{Status: string(status), LastSeen: lastSeen})
	if err != nil {
		return err
	}
	return r.client.Set(ctx, statusKeyPrefix+deviceID, payload, r.ttl).Err()
}

// GetDeviceStatus reads a device's cached status. The boolean is false when no
// entry exists (cache miss).
func (r *Redis) GetDeviceStatus(ctx context.Context, deviceID string) (models.DeviceStatus, time.Time, bool, error) {
	raw, err := r.client.Get(ctx, statusKeyPrefix+deviceID).Bytes()
	if err == redis.Nil {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	var entry statusEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return "", time.Time{}, false, err
	}
	return models.DeviceStatus(entry.Status), entry.LastSeen, true, nil
}

// Broadcast publishes an event to the shared events channel. It implements the
// service.Publisher interface so the service can fan out across instances.
func (r *Redis) Broadcast(event models.Event) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = r.client.Publish(ctx, eventsChannel, payload).Err()
}

// Subscribe consumes events from the shared channel and forwards them to the
// handler until ctx is cancelled. Intended to run in its own goroutine.
func (r *Redis) Subscribe(ctx context.Context, handler func(models.Event)) error {
	sub := r.client.Subscribe(ctx, eventsChannel)
	defer sub.Close()

	if _, err := sub.Receive(ctx); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			var event models.Event
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				continue
			}
			handler(event)
		}
	}
}
