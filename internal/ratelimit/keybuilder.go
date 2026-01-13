// Package ratelimit provides key construction.
package ratelimit

// KeyBuilder builds request keys.
type KeyBuilder struct {
	bufPool *ByteBufferPool
}

// BuildKey builds a stable key for a request.
func (kb *KeyBuilder) BuildKey(tenantID, userID, resource string) []byte {
	if kb == nil || kb.bufPool == nil {
		return []byte(tenantID + "\x1f" + userID + "\x1f" + resource)
	}
	buf := kb.bufPool.Get()
	buf = append(buf, tenantID...)
	buf = append(buf, '\x1f')
	buf = append(buf, userID...)
	buf = append(buf, '\x1f')
	buf = append(buf, resource...)
	return buf
}

// ReleaseKey returns a buffer to the pool.
func (kb *KeyBuilder) ReleaseKey(b []byte) {
	if kb == nil || kb.bufPool == nil {
		return
	}
	kb.bufPool.Put(b)
}

// KeyToString converts key bytes to a string.
func (kb *KeyBuilder) KeyToString(b []byte) string {
	return string(b)
}
