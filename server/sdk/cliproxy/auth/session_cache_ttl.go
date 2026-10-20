package auth

import (
	"strings"
	"time"
)

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

// CountByAuth returns the number of live conversations bound to each auth ID.
// Cache keys are provider::session::model; the model suffix is ignored, so one
// conversation that calls several models (Claude Code also calling haiku)
// counts once, and an alias group counts once. Expired bindings are skipped.
func (c *SessionCache) CountByAuth() map[string]int {
	counts := make(map[string]int)
	if c == nil {
		return counts
	}
	now := time.Now()
	c.mu.RLock()
	defer c.mu.RUnlock()
	seen := make(map[string]map[string]struct{})
	for _, group := range c.groups {
		if group.authID == "" || !now.Before(group.expiresAt) {
			continue
		}
		conversations := seen[group.authID]
		if conversations == nil {
			conversations = make(map[string]struct{})
			seen[group.authID] = conversations
		}
		known := false
		for _, alias := range group.aliases {
			key := sessionConversationKey(alias)
			if _, ok := conversations[key]; ok {
				known = true
			}
			conversations[key] = struct{}{}
		}
		if !known {
			counts[group.authID]++
		}
	}
	return counts
}

// sessionConversationKey strips the model suffix from a provider::session::model
// cache key. Keys without a model segment are returned unchanged.
func sessionConversationKey(cacheKey string) string {
	parts := strings.Split(cacheKey, "::")
	if len(parts) < 3 {
		return cacheKey
	}
	return strings.Join(parts[:len(parts)-1], "::")
}
