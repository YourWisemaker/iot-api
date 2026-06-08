// Package realtime provides a publish/subscribe hub for broadcasting events
// to connected WebSocket clients.
package realtime

import (
	"sync"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// Hub fans out events to all registered subscribers. It is safe for
// concurrent use by multiple goroutines.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[chan models.Event]struct{}
	bufferSize  int
}

// NewHub creates a hub. bufferSize is the per-subscriber channel buffer; slow
// subscribers that fill their buffer will drop events rather than block the hub.
func NewHub(bufferSize int) *Hub {
	if bufferSize <= 0 {
		bufferSize = 16
	}
	return &Hub{
		subscribers: make(map[chan models.Event]struct{}),
		bufferSize:  bufferSize,
	}
}

// Subscribe registers a new subscriber and returns its event channel.
func (h *Hub) Subscribe() chan models.Event {
	ch := make(chan models.Event, h.bufferSize)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (h *Hub) Unsubscribe(ch chan models.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subscribers[ch]; ok {
		delete(h.subscribers, ch)
		close(ch)
	}
}

// Broadcast delivers an event to every subscriber without blocking. Events
// destined for a full subscriber buffer are dropped for that subscriber.
func (h *Hub) Broadcast(event models.Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			// Subscriber is slow; drop to protect the hub.
		}
	}
}

// SubscriberCount returns the number of active subscribers.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}
