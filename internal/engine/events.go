package engine

import "sync"

type EventType string

const (
	EventTaskUpdated  EventType = "task.updated"
	EventTaskProgress EventType = "task.progress"
	EventTaskRemoved  EventType = "task.removed"
	EventAppReady     EventType = "app.ready"
)

type Event struct {
	Type EventType   `json:"type"`
	Data interface{} `json:"data"`
}

type EventBus struct {
	mu   sync.RWMutex
	subs map[chan Event]struct{}
}

func NewEventBus() *EventBus {
	return &EventBus{subs: make(map[chan Event]struct{})}
}

func (b *EventBus) Subscribe(buf int) chan Event {
	if buf < 1 {
		buf = 16
	}
	ch := make(chan Event, buf)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *EventBus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

func (b *EventBus) Publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// drop if subscriber is slow; progress is coalesced upstream
		}
	}
}
