// Package ratelimit provides LRU key tracking.
package ratelimit

import "container/list"

// LRUKeys tracks keys in LRU order.
type LRUKeys struct {
	max   int
	items map[string]*list.Element
	list  *list.List
}

// NewLRUKeys constructs an LRUKeys tracker.
func NewLRUKeys(max int) *LRUKeys {
	if max < 0 {
		max = 0
	}
	return &LRUKeys{
		max:   max,
		items: make(map[string]*list.Element),
		list:  list.New(),
	}
}

// Touch marks a key as most recently used.
func (lru *LRUKeys) Touch(key string) {
	if lru == nil {
		return
	}
	if element, ok := lru.items[key]; ok {
		lru.list.MoveToFront(element)
		return
	}
	lru.Add(key)
}

// Add inserts a key as most recently used.
func (lru *LRUKeys) Add(key string) {
	if lru == nil {
		return
	}
	if element, ok := lru.items[key]; ok {
		lru.list.MoveToFront(element)
		return
	}
	element := lru.list.PushFront(key)
	lru.items[key] = element
}

// Remove deletes a key.
func (lru *LRUKeys) Remove(key string) {
	if lru == nil {
		return
	}
	element, ok := lru.items[key]
	if !ok {
		return
	}
	lru.list.Remove(element)
	delete(lru.items, key)
}

// EvictIfNeeded evicts least recently used keys until size <= max.
func (lru *LRUKeys) EvictIfNeeded() []string {
	if lru == nil {
		return nil
	}
	if len(lru.items) <= lru.max {
		return nil
	}

	count := len(lru.items) - lru.max
	evicted := make([]string, 0, count)
	for i := 0; i < count; i++ {
		element := lru.list.Back()
		if element == nil {
			break
		}
		key := element.Value.(string)
		evicted = append(evicted, key)
		lru.list.Remove(element)
		delete(lru.items, key)
	}
	return evicted
}
