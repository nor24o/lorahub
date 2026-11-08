// Package events provides an in-process publish/subscribe bus used to fan
// out events to the live-data websocket, integrations and storage.
package events

import (
	"sync"

	"github.com/nor24o/lorahub/internal/model"
)

// Filter decides whether a subscriber receives an event.
type Filter func(*model.Event) bool

type sub struct {
	ch     chan *model.Event
	filter Filter
}

// Bus is a non-blocking fan-out bus. Slow subscribers drop events.
type Bus struct {
	mu   sync.RWMutex
	subs map[*sub]struct{}
}

// New creates a bus.
func New() *Bus { return &Bus{subs: map[*sub]struct{}{}} }

// Subscribe registers a subscriber. Call the returned func to unsubscribe.
func (b *Bus) Subscribe(buf int, f Filter) (<-chan *model.Event, func()) {
	s := &sub{ch: make(chan *model.Event, buf), filter: f}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s.ch, func() {
		b.mu.Lock()
		if _, ok := b.subs[s]; ok {
			delete(b.subs, s)
			close(s.ch)
		}
		b.mu.Unlock()
	}
}

// Publish sends an event to all matching subscribers.
func (b *Bus) Publish(e *model.Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		if s.filter != nil && !s.filter(e) {
			continue
		}
		select {
		case s.ch <- e:
		default:
		}
	}
}
