package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// newTestRedis spins up an in-process miniredis and returns a connected client.
func newTestRedis(t *testing.T) (*Redis, *miniredis.Miniredis) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis: %v", err)
	}
	r, err := New(context.Background(), Config{Addr: mr.Addr(), StatusTTL: time.Minute})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		mr.Close()
	})
	return r, mr
}

func TestSetAndGetDeviceStatus(t *testing.T) {
	r, _ := newTestRedis(t)
	ctx := context.Background()
	seen := time.Now().UTC().Truncate(time.Second)

	if err := r.SetDeviceStatus(ctx, "dev-1", models.StatusOnline, seen); err != nil {
		t.Fatalf("set: %v", err)
	}

	status, lastSeen, ok, err := r.GetDeviceStatus(ctx, "dev-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok {
		t.Fatal("expected cache hit")
	}
	if status != models.StatusOnline {
		t.Fatalf("expected online, got %s", status)
	}
	if !lastSeen.Equal(seen) {
		t.Fatalf("expected last seen %v, got %v", seen, lastSeen)
	}
}

func TestGetDeviceStatusMiss(t *testing.T) {
	r, _ := newTestRedis(t)
	_, _, ok, err := r.GetDeviceStatus(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestStatusTTLExpiry(t *testing.T) {
	r, mr := newTestRedis(t)
	ctx := context.Background()
	if err := r.SetDeviceStatus(ctx, "dev-1", models.StatusOnline, time.Now()); err != nil {
		t.Fatalf("set: %v", err)
	}
	// Fast-forward miniredis past the TTL.
	mr.FastForward(2 * time.Minute)
	if _, _, ok, _ := r.GetDeviceStatus(ctx, "dev-1"); ok {
		t.Fatal("expected entry to expire")
	}
}

func TestBroadcastAndSubscribe(t *testing.T) {
	r, _ := newTestRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		mu       sync.Mutex
		received []models.Event
	)
	ready := make(chan struct{})
	go func() {
		// Signal once the subscription loop is active.
		close(ready)
		_ = r.Subscribe(ctx, func(e models.Event) {
			mu.Lock()
			received = append(received, e)
			mu.Unlock()
		})
	}()
	<-ready
	// Give the subscriber a moment to register with Redis.
	time.Sleep(100 * time.Millisecond)

	r.Broadcast(models.Event{Type: "telemetry", DeviceID: "dev-1", Timestamp: time.Now()})

	// Poll for delivery.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("expected to receive a broadcast event")
	}
	if received[0].DeviceID != "dev-1" {
		t.Fatalf("unexpected event: %+v", received[0])
	}
}
