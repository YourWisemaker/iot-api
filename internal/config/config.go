// Package config loads runtime configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds all tunable runtime settings.
type Config struct {
	HTTPAddr        string
	Workers         int
	QueueSize       int
	MaxHistory      int
	OfflineAfter    time.Duration
	ReconcileEvery  time.Duration
	MQTTBrokerURL   string
	MQTTTopicPrefix string
	MQTTClientID    string
	MQTTUsername    string
	MQTTPassword    string

	// MongoDB (NoSQL) persistence. When MongoURI is empty the in-memory store
	// is used instead.
	MongoURI string
	MongoDB  string

	// Redis cache + distributed event bus. When RedisAddr is empty, status is
	// served from the primary store and events are broadcast locally only.
	RedisAddr      string
	RedisPassword  string
	RedisDB        int
	RedisStatusTTL time.Duration

	// JWT authentication. When JWTSecret is empty, authentication is disabled.
	JWTSecret  string
	JWTTTL     time.Duration // access-token lifetime
	RefreshTTL time.Duration // refresh-token lifetime

	// Bootstrap admin user, seeded at startup when it does not already exist.
	AdminUsername string
	AdminPassword string
}

// Load reads configuration from the environment, applying sensible defaults.
func Load() Config {
	return Config{
		HTTPAddr:        getEnv("HTTP_ADDR", ":8080"),
		Workers:         getEnvInt("WORKERS", 8),
		QueueSize:       getEnvInt("QUEUE_SIZE", 1024),
		MaxHistory:      getEnvInt("MAX_HISTORY", 1000),
		OfflineAfter:    getEnvDuration("OFFLINE_AFTER", 30*time.Second),
		ReconcileEvery:  getEnvDuration("RECONCILE_EVERY", 10*time.Second),
		MQTTBrokerURL:   getEnv("MQTT_BROKER_URL", ""),
		MQTTTopicPrefix: getEnv("MQTT_TOPIC_PREFIX", "devices"),
		MQTTClientID:    getEnv("MQTT_CLIENT_ID", ""),
		MQTTUsername:    getEnv("MQTT_USERNAME", ""),
		MQTTPassword:    getEnv("MQTT_PASSWORD", ""),

		MongoURI: getEnv("MONGO_URI", ""),
		MongoDB:  getEnv("MONGO_DB", "iot"),

		RedisAddr:      getEnv("REDIS_ADDR", ""),
		RedisPassword:  getEnv("REDIS_PASSWORD", ""),
		RedisDB:        getEnvInt("REDIS_DB", 0),
		RedisStatusTTL: getEnvDuration("REDIS_STATUS_TTL", 5*time.Minute),

		JWTSecret:     getEnv("JWT_SECRET", ""),
		JWTTTL:        getEnvDuration("JWT_TTL", 15*time.Minute),
		RefreshTTL:    getEnvDuration("JWT_REFRESH_TTL", 7*24*time.Hour),
		AdminUsername: getEnv("ADMIN_USERNAME", "admin"),
		AdminPassword: getEnv("ADMIN_PASSWORD", ""),
	}
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
