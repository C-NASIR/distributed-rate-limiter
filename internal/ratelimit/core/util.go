// Package core provides utility helpers.
package core

// limiterPoolKey builds a map key for limiter entries.
func limiterPoolKey(tenantID, resource string) string {
	return tenantID + "\x1f" + resource
}
