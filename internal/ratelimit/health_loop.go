// Package ratelimit provides periodic health updates.
package ratelimit

import (
	"context"
	"errors"
	"time"
)

// HealthLoop periodically updates the degrade controller.
type HealthLoop struct {
	degrade  *DegradeController
	interval time.Duration
}

// Start begins the health update loop.
func (h *HealthLoop) Start(ctx context.Context) error {
	if h == nil || h.degrade == nil {
		return errors.New("health loop is not configured")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	interval := h.interval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			h.degrade.Update(ctx)
		}
	}
}
