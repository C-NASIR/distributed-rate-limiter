// Package ratelimit provides an in-memory pubsub implementation.
package ratelimit

import (
	"context"
	"errors"
	"sync"
)

// InMemoryPubSub delivers messages to in-process subscribers.
type InMemoryPubSub struct {
	mu    sync.Mutex
	subs  map[string]map[int]*pubSubSubscription
	next  int
}

type pubSubSubscription struct {
	ctx     context.Context
	handler func(context.Context, []byte)
}

// NewInMemoryPubSub constructs an in-memory pubsub.
func NewInMemoryPubSub() *InMemoryPubSub {
	return &InMemoryPubSub{subs: make(map[string]map[int]*pubSubSubscription)}
}

// Subscribe registers a handler for a channel.
func (ps *InMemoryPubSub) Subscribe(ctx context.Context, channel string, handler func(context.Context, []byte)) error {
	if ps == nil {
		return errors.New("pubsub is nil")
	}
	if channel == "" {
		return errors.New("channel is required")
	}
	if handler == nil {
		return errors.New("handler is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	ps.mu.Lock()
	if ps.subs == nil {
		ps.subs = make(map[string]map[int]*pubSubSubscription)
	}
	ps.next++
	id := ps.next
	if ps.subs[channel] == nil {
		ps.subs[channel] = make(map[int]*pubSubSubscription)
	}
	ps.subs[channel][id] = &pubSubSubscription{ctx: ctx, handler: handler}
	ps.mu.Unlock()

	go func() {
		<-ctx.Done()
		ps.remove(channel, id)
	}()

	return nil
}

// Publish delivers payloads to current subscribers.
func (ps *InMemoryPubSub) Publish(ctx context.Context, channel string, payload []byte) error {
	if ps == nil {
		return errors.New("pubsub is nil")
	}
	if channel == "" {
		return errors.New("channel is required")
	}

	ps.mu.Lock()
	subs := ps.subs[channel]
	copySubs := make([]*pubSubSubscription, 0, len(subs))
	for _, sub := range subs {
		copySubs = append(copySubs, sub)
	}
	ps.mu.Unlock()

	for _, sub := range copySubs {
		if sub == nil {
			continue
		}
		if sub.ctx != nil && sub.ctx.Err() != nil {
			continue
		}
		data := make([]byte, len(payload))
		copy(data, payload)
		go sub.handler(sub.ctx, data)
	}

	return nil
}

func (ps *InMemoryPubSub) remove(channel string, id int) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.subs == nil {
		return
	}
	subs := ps.subs[channel]
	if subs == nil {
		return
	}
	delete(subs, id)
	if len(subs) == 0 {
		delete(ps.subs, channel)
	}
}
