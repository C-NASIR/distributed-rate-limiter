// Package ratelimit provides utility helpers.
package ratelimit

// limiterPoolKey builds a map key for limiter entries.
func limiterPoolKey(tenantID, resource string) string {
	return tenantID + "\x1f" + resource
}
