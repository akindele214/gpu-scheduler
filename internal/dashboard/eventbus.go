package dashboard

import (
	"sync"
)

type EventBus struct {
	mu          sync.RWMutex
	subscribers map[chan SSEEvent]struct{}
}

func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan SSEEvent]struct{}),
	}
}

func (eb *EventBus) Subscribe() chan SSEEvent {
	bufferedChan := make(chan SSEEvent, 64)

	eb.mu.Lock()
	eb.subscribers[bufferedChan] = struct{}{}
	eb.mu.Unlock()

	return bufferedChan
}

func (eb *EventBus) Unsubscribe(ch chan SSEEvent) {
	eb.mu.Lock()
	if _, ok := eb.subscribers[ch]; ok {
		delete(eb.subscribers, ch)
		close(ch)
	}
	eb.mu.Unlock()
}

func (b *EventBus) Publish(event SSEEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for ch := range b.subscribers {
		select {
		case ch <- event:
			// sent successfully
		default:
			// channel full — drop event for this subscriber
			// prevents slow client from blocking scheduler
		}
	}
}
