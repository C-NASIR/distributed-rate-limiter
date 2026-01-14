// Package core provides byte buffer pooling.
package core

import "sync"

// ByteBufferPool pools byte slices for key construction.
type ByteBufferPool struct {
	pool   sync.Pool
	maxCap int
}

// NewByteBufferPool constructs a byte buffer pool.
func NewByteBufferPool(maxCap int) *ByteBufferPool {
	if maxCap <= 0 {
		maxCap = 4096
	}
	return &ByteBufferPool{maxCap: maxCap}
}

// Get returns a byte slice with zero length.
func (p *ByteBufferPool) Get() []byte {
	if p == nil {
		return make([]byte, 0)
	}
	if buf, ok := p.pool.Get().([]byte); ok {
		if cap(buf) > p.maxCap {
			return make([]byte, 0, p.maxCap)
		}
		return buf[:0]
	}
	return make([]byte, 0, p.maxCap)
}

// Put returns a byte slice to the pool.
func (p *ByteBufferPool) Put(b []byte) {
	if p == nil || b == nil {
		return
	}
	if cap(b) > p.maxCap {
		return
	}
	p.pool.Put(b[:0])
}
