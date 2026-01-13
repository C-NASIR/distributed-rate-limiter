// Package ratelimit provides cache invalidation subscribers.
package ratelimit

import (
	"context"
	"errors"
)

// CacheInvalidator subscribes to invalidation events.
type CacheInvalidator struct {
	db      RuleDB
	rules   *RuleCache
	pool    *LimiterPool
	pubsub  PubSub
	channel string
}

// Subscribe registers the invalidation handler.
func (c *CacheInvalidator) Subscribe(ctx context.Context) error {
	if c == nil || c.pubsub == nil {
		return errors.New("cache invalidator is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	err := c.pubsub.Subscribe(ctx, c.channel, func(handlerCtx context.Context, payload []byte) {
		c.handle(handlerCtx, payload)
	})
	if err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}

func (c *CacheInvalidator) handle(ctx context.Context, payload []byte) {
	if c == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	event, err := UnmarshalInvalidationEvent(payload)
	if err != nil {
		return
	}
	switch event.Action {
	case "delete":
		if c.rules != nil {
			c.rules.DeleteIfOlderOrEqual(event.TenantID, event.Resource, event.Version)
		}
		if c.pool != nil {
			c.pool.Cutover(ctx, event.TenantID, event.Resource, event.Version)
		}
	case "upsert":
		if c.db != nil {
			rule, err := c.db.Get(ctx, event.TenantID, event.Resource)
			if err == nil && rule != nil {
				if c.rules != nil {
					c.rules.UpsertIfNewer(rule)
				}
			}
		}
		if c.pool != nil {
			c.pool.Cutover(ctx, event.TenantID, event.Resource, event.Version)
		}
	}
}
