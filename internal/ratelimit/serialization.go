// Package ratelimit handles invalidation event serialization.
package ratelimit

import (
	"encoding/json"
	"time"
)

// InvalidationEvent describes a cache invalidation broadcast.
type InvalidationEvent struct {
	TenantID  string    `json:"tenant_id"`
	Resource  string    `json:"resource"`
	Action    string    `json:"action"`
	Version   int64     `json:"version"`
	Timestamp time.Time `json:"timestamp"`
}

// MarshalInvalidationEvent serializes an invalidation event.
func MarshalInvalidationEvent(e InvalidationEvent) ([]byte, error) {
	return json.Marshal(e)
}

// UnmarshalInvalidationEvent deserializes an invalidation event.
func UnmarshalInvalidationEvent(b []byte) (InvalidationEvent, error) {
	var event InvalidationEvent
	err := json.Unmarshal(b, &event)
	return event, err
}
