// Package ratelimit provides response pooling.
package ratelimit

import "sync"

// ResponsePool pools CheckLimitResponse values.
type ResponsePool struct {
	pool sync.Pool
}

// NewResponsePool constructs a response pool.
func NewResponsePool() *ResponsePool {
	return &ResponsePool{pool: sync.Pool{New: func() any {
		return &CheckLimitResponse{}
	}}}
}

// Get returns a reset response.
func (rp *ResponsePool) Get() *CheckLimitResponse {
	if rp == nil {
		return &CheckLimitResponse{}
	}
	resp := rp.pool.Get().(*CheckLimitResponse)
	resetCheckLimitResponse(resp)
	return resp
}

// Put resets and returns a response to the pool.
func (rp *ResponsePool) Put(resp *CheckLimitResponse) {
	if rp == nil || resp == nil {
		return
	}
	resetCheckLimitResponse(resp)
	rp.pool.Put(resp)
}

func resetCheckLimitResponse(resp *CheckLimitResponse) {
	if resp == nil {
		return
	}
	resp.Allowed = false
	resp.Remaining = 0
	resp.Limit = 0
	resp.ResetAfter = 0
	resp.RetryAfter = 0
	resp.ErrorCode = ""
}
