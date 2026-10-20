package auth

import "time"

// SetTTLResolver installs a per-key TTL. The resolver receives the cache key
// (provider::session::model) and returns the TTL for that binding; a nil
// resolver or a non-positive result falls back to the cache default.
func (c *SessionCache) SetTTLResolver(fn func(cacheKey string) time.Duration) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.ttlResolver = fn
	c.minResolvedTTL = 0
	c.mu.Unlock()
}

// ttlFor returns the TTL to apply to cacheKey and tracks the smallest TTL
// handed out so the cleanup loop can keep pace with short-lived bindings.
// Must be called with c.mu held.
func (c *SessionCache) ttlFor(cacheKey string) time.Duration {
	if c.ttlResolver == nil {
		return c.ttl
	}
	ttl := c.ttlResolver(cacheKey)
	if ttl <= 0 {
		return c.ttl
	}
	if c.minResolvedTTL == 0 || ttl < c.minResolvedTTL {
		c.minResolvedTTL = ttl
	}
	return ttl
}

// cleanupInterval is half the smallest TTL in effect, floored at 1ms.
func (c *SessionCache) cleanupInterval() time.Duration {
	c.mu.RLock()
	ttl := c.ttl
	if c.minResolvedTTL > 0 && c.minResolvedTTL < ttl {
		ttl = c.minResolvedTTL
	}
	c.mu.RUnlock()
	interval := ttl / 2
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	return interval
}

// CountByAuth returns the number of live sessions bound to each auth ID. A
// session with several aliases counts once; expired bindings are skipped.
func (c *SessionCache) CountByAuth() map[string]int {
	counts := make(map[string]int)
	if c == nil {
		return counts
	}
	now := time.Now()
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, group := range c.groups {
		if group.authID == "" || !now.Before(group.expiresAt) {
			continue
		}
		counts[group.authID]++
	}
	return counts
}
