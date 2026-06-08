// Command server runs the IoT Device Management Platform API.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/YourWisemaker/iot-api/internal/alerts"
	"github.com/YourWisemaker/iot-api/internal/api"
	"github.com/YourWisemaker/iot-api/internal/auth"
	"github.com/YourWisemaker/iot-api/internal/cache"
	"github.com/YourWisemaker/iot-api/internal/config"
	"github.com/YourWisemaker/iot-api/internal/models"
	"github.com/YourWisemaker/iot-api/internal/mqtt"
	"github.com/YourWisemaker/iot-api/internal/realtime"
	"github.com/YourWisemaker/iot-api/internal/service"
	"github.com/YourWisemaker/iot-api/internal/store"
	"github.com/YourWisemaker/iot-api/internal/worker"
)

func main() {
	cfg := config.Load()
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[iot-api] ")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Storage backend: MongoDB (NoSQL) when configured, otherwise in-memory.
	var st store.Store
	if cfg.MongoURI != "" {
		ms, err := store.NewMongoStore(ctx, store.MongoConfig{
			URI:        cfg.MongoURI,
			Database:   cfg.MongoDB,
			MaxHistory: cfg.MaxHistory,
		})
		if err != nil {
			log.Fatalf("mongo: connect: %v", err)
		}
		defer ms.Close(context.Background())
		st = ms
		log.Printf("store: using MongoDB at %s (db=%s)", cfg.MongoURI, cfg.MongoDB)
	} else {
		st = store.NewMemoryStore(cfg.MaxHistory)
		log.Printf("store: using in-memory store (set MONGO_URI to use MongoDB)")
	}

	pool := worker.NewPool(cfg.Workers, cfg.QueueSize)
	pool.Start()
	defer pool.Stop()

	hub := realtime.NewHub(64)

	// Default alert rules; extend via the alerts engine as needed.
	engine := alerts.NewEngine(
		models.AlertRule{Metric: "temperature", Operator: ">", Threshold: 80, Severity: models.SeverityCritical},
		models.AlertRule{Metric: "battery", Operator: "<", Threshold: 15, Severity: models.SeverityWarning},
		models.AlertRule{Metric: "humidity", Operator: ">", Threshold: 95, Severity: models.SeverityWarning},
	)

	// Optional Redis: status cache + distributed event bus. When enabled, the
	// service publishes events to Redis and a subscriber re-broadcasts them to
	// this instance's local hub (so all instances stay in sync).
	var svcOpts []service.Option
	if cfg.RedisAddr != "" {
		rc, err := cache.New(ctx, cache.Config{
			Addr:      cfg.RedisAddr,
			Password:  cfg.RedisPassword,
			DB:        cfg.RedisDB,
			StatusTTL: cfg.RedisStatusTTL,
		})
		if err != nil {
			log.Fatalf("redis: connect: %v", err)
		}
		defer rc.Close()
		svcOpts = append(svcOpts, service.WithPublisher(rc), service.WithStatusCache(rc))
		go func() {
			if err := rc.Subscribe(ctx, hub.Broadcast); err != nil && ctx.Err() == nil {
				log.Printf("redis: subscribe ended: %v", err)
			}
		}()
		log.Printf("redis: enabled at %s (status cache + event bus)", cfg.RedisAddr)
	} else {
		log.Printf("redis: disabled (set REDIS_ADDR to enable)")
	}

	svc := service.New(st, pool, engine, hub, cfg.OfflineAfter, svcOpts...)

	// Optional JWT authentication.
	authManager := auth.NewManager(auth.Config{
		Secret:   cfg.JWTSecret,
		TTL:      cfg.JWTTTL,
		Username: cfg.AuthUsername,
		Password: cfg.AuthPassword,
	})
	if authManager == nil {
		log.Printf("auth: disabled (set JWT_SECRET to require authentication)")
	} else if !authManager.LoginEnabled() {
		log.Printf("auth: enabled, but login disabled (set AUTH_PASSWORD to issue tokens)")
	} else {
		log.Printf("auth: enabled (JWT required on protected routes)")
	}

	// Background status reconciliation.
	go svc.RunStatusReconciler(ctx, cfg.ReconcileEvery)

	// Optional MQTT bridge.
	var bridge *mqtt.Bridge
	if cfg.MQTTBrokerURL != "" {
		bridge = mqtt.NewBridge(mqtt.Config{
			BrokerURL:   cfg.MQTTBrokerURL,
			ClientID:    cfg.MQTTClientID,
			TopicPrefix: cfg.MQTTTopicPrefix,
			Username:    cfg.MQTTUsername,
			Password:    cfg.MQTTPassword,
		}, svc)
		if err := bridge.Connect(); err != nil {
			log.Printf("mqtt: initial connect failed (will retry): %v", err)
		}
		defer bridge.Disconnect()
	} else {
		log.Printf("mqtt: disabled (set MQTT_BROKER_URL to enable)")
	}

	// HTTP server.
	handler := api.NewHandler(svc, authManager)
	srv := &http.Server{
		Addr:         cfg.HTTPAddr,
		Handler:      handler.Routes(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // WebSocket connections are long-lived.
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Printf("http: listening on %s", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	// Graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Printf("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http: shutdown error: %v", err)
	}
}
