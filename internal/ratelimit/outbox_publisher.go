// Package ratelimit publishes outbox events.
package ratelimit

import (
	"context"
	"errors"
	"time"
)

// OutboxPublisher reads from an outbox and publishes to pubsub.
type OutboxPublisher struct {
	outbox   Outbox
	pubsub   PubSub
	channel  string
	interval time.Duration
}

// Start begins the publishing loop.
func (p *OutboxPublisher) Start(ctx context.Context) error {
	if p == nil || p.outbox == nil || p.pubsub == nil {
		return errors.New("outbox publisher is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	interval := p.interval
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			rows, err := p.outbox.FetchPending(ctx, 100)
			if err != nil {
				continue
			}
			for _, row := range rows {
				if err := p.pubsub.Publish(ctx, p.channel, row.Data); err != nil {
					continue
				}
				_ = p.outbox.MarkSent(ctx, row.ID)
			}
		}
	}
}
