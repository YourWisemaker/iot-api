package realtime

import (
	"sync"
	"testing"
	"time"

	"github.com/YourWisemaker/iot-api/internal/models"
)

func TestSubscribeReceivesBroadcast(t *testing.T) {
	h := NewHub(8)
	sub := h.Subscribe()
	defer h.Unsubscribe(sub)

	h.Broadcast(models.Event{Type: "telemetry", DeviceID: "d1"})

	select {
	case ev := <-sub:
		if ev.DeviceID != "d1" {
			t.Fatalf("unexpected event %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	h := NewHub(8)
	a := h.Subscribe()
	b := h.Subscribe()
	defer h.Unsubscribe(a)
	defer h.Unsubscribe(b)

	if h.SubscriberCount() != 2 {
		t.Fatalf("expected 2 subscribers, got %d", h.SubscriberCount())
	}

	h.Broadcast(models.Event{Type: "status"})
	for _, ch := range []chan models.Event{a, b} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("subscriber did not receive event")
		}
	}
}

func TestUnsubscribeClosesChannel(t *testing.T) {
	h := NewHub(8)
	sub := h.Subscribe()
	h.Unsubscribe(sub)

	if _, open := <-sub; open {
		t.Fatal("expected channel to be closed after unsubscribe")
	}
	if h.SubscriberCount() != 0 {
		t.Fatal("expected no subscribers")
	}
	// Double unsubscribe must not panic.
	h.Unsubscribe(sub)
}

func TestBroadcastDoesNotBlockOnSlowSubscriber(t *testing.T) {
	h := NewHub(1) // buffer of 1
	sub := h.Subscribe()
	defer h.Unsubscribe(sub)

	// Send more than the buffer can hold; excess is dropped, not blocked.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			h.Broadcast(models.Event{Type: "telemetry"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast blocked on slow subscriber")
	}
}

func TestConcurrentSubscribeBroadcast(t *testing.T) {
	h := NewHub(16)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch := h.Subscribe()
			h.Broadcast(models.Event{Type: "telemetry"})
			h.Unsubscribe(ch)
		}()
	}
	wg.Wait()
}
