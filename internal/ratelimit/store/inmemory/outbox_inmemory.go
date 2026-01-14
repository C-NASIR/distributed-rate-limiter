// Package inmemory provides in-memory outbox storage.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ratelimit/internal/ratelimit/core"
)

// InMemoryOutbox stores outbox rows in memory.
type InMemoryOutbox struct {
	mu      sync.Mutex
	entries []outboxEntry
	counter int64
}

type outboxEntry struct {
	row  core.OutboxRow
	sent bool
}

// NewInMemoryOutbox constructs an in-memory outbox.
func NewInMemoryOutbox() *InMemoryOutbox {
	return &InMemoryOutbox{}
}

// Insert appends an outbox row.
func (o *InMemoryOutbox) Insert(ctx context.Context, data []byte) (string, error) {
	if o == nil {
		return "", errors.New("outbox is nil")
	}
	rowData := make([]byte, len(data))
	copy(rowData, data)
	id := fmt.Sprintf("%d", atomic.AddInt64(&o.counter, 1))

	o.mu.Lock()
	defer o.mu.Unlock()
	o.entries = append(o.entries, outboxEntry{row: core.OutboxRow{ID: id, Data: rowData}})
	return id, nil
}

// FetchPending returns oldest pending rows.
func (o *InMemoryOutbox) FetchPending(ctx context.Context, limit int) ([]core.OutboxRow, error) {
	if o == nil {
		return nil, errors.New("outbox is nil")
	}
	if limit <= 0 {
		return []core.OutboxRow{}, nil
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	rows := make([]core.OutboxRow, 0, limit)
	for _, entry := range o.entries {
		if entry.sent {
			continue
		}
		rows = append(rows, entry.row)
		if len(rows) >= limit {
			break
		}
	}
	return rows, nil
}

// MarkSent marks a row as sent.
func (o *InMemoryOutbox) MarkSent(ctx context.Context, id string) error {
	if o == nil {
		return errors.New("outbox is nil")
	}
	if id == "" {
		return errors.New("id is required")
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	for i := range o.entries {
		if o.entries[i].row.ID == id {
			o.entries[i].sent = true
			return nil
		}
	}
	return errors.New("outbox row not found")
}
