package auth

import (
	"context"
	"strings"
	"time"
)

// BindingOverrider is implemented by fallback selectors that can pull an
// active session off its bound credential. SessionAffinitySelector consults it
// on every cache hit whose bound auth is still available.
type BindingOverrider interface {
	// OverrideBinding returns a different auth the session must move to now,
	// or false to keep the binding. available is the availability-filtered
	// candidate set; the returned auth must come from it.
	OverrideBinding(provider, model, boundAuthID string, available []*Auth) (*Auth, bool)
}

// OverrideBinding moves a session to the urgent account (rank 1, weekly reset
// inside the urgency window, headroom left) when that is not the bound one.
func (s *ResetAwareSelector) OverrideBinding(provider, model, boundAuthID string, available []*Auth) (*Auth, bool) {
	if s == nil {
		return nil, false
	}
	urgent, ok := s.Urgent(provider, model, available)
	if !ok || urgent == nil || urgent.ID == "" || urgent.ID == boundAuthID {
		return nil, false
	}
	return urgent, true
}

// Fallback returns the selector consulted for cold sessions and failover.
func (s *SessionAffinitySelector) Fallback() Selector {
	if s == nil {
		return nil
	}
	return s.fallback
}

// BindingCounts returns the number of live sessions bound to each auth ID.
func (s *SessionAffinitySelector) BindingCounts() map[string]int {
	if s == nil || s.cache == nil {
		return map[string]int{}
	}
	return s.cache.CountByAuth()
}

// ProviderTTLs returns a copy of the per-provider idle TTLs in effect.
func (s *SessionAffinitySelector) ProviderTTLs() map[string]time.Duration {
	out := make(map[string]time.Duration)
	if s == nil {
		return out
	}
	for provider, ttl := range s.providerTTL {
		out[provider] = ttl
	}
	return out
}

// usesResetAwareFallback reports whether the fallback ranks across every
// priority tier itself, in which case the affinity wrapper must not narrow
// its candidates to the highest tier.
func (s *SessionAffinitySelector) usesResetAwareFallback() bool {
	if s == nil {
		return false
	}
	_, ok := s.fallback.(*ResetAwareSelector)
	return ok
}

// fallbackCandidates narrows an across-priority availability slice for the
// fallback selector: upstream selectors only see the highest tier, while the
// reset-aware selector sees every tier so a limited high-priority account
// cannot hide a usable lower one.
func (s *SessionAffinitySelector) fallbackCandidates(available []*Auth) []*Auth {
	if s.usesResetAwareFallback() {
		return available
	}
	return highestPriorityAuths(available)
}

// noSessionCandidates resolves availability for requests without a session ID.
func (s *SessionAffinitySelector) noSessionCandidates(ctx context.Context, auths []*Auth, provider, model string, now time.Time) ([]*Auth, error) {
	if s.usesResetAwareFallback() {
		return getSelectorAvailableAuthsAcrossPriorities(ctx, auths, provider, model, now)
	}
	return getSelectorAvailableAuths(ctx, auths, provider, model, now)
}

// overrideBinding asks a BindingOverrider fallback whether the session must
// leave its bound auth. The result is always one of fallbackAuths.
func (s *SessionAffinitySelector) overrideBinding(provider, model, boundAuthID string, fallbackAuths []*Auth) (*Auth, bool) {
	if s == nil || len(fallbackAuths) == 0 {
		return nil, false
	}
	overrider, ok := s.fallback.(BindingOverrider)
	if !ok || overrider == nil {
		return nil, false
	}
	target, ok := overrider.OverrideBinding(provider, model, boundAuthID, fallbackAuths)
	if !ok || target == nil || target.ID == "" || target.ID == boundAuthID {
		return nil, false
	}
	for _, candidate := range fallbackAuths {
		if candidate != nil && candidate.ID == target.ID {
			return candidate, true
		}
	}
	return nil, false
}

// normalizeProviderTTL lowercases provider keys and drops empty or
// non-positive entries. It returns nil when nothing remains.
func normalizeProviderTTL(ttls map[string]time.Duration) map[string]time.Duration {
	normalized := make(map[string]time.Duration, len(ttls))
	for provider, ttl := range ttls {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if provider == "" || ttl <= 0 {
			continue
		}
		normalized[provider] = ttl
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// providerTTLResolver maps a cache key (provider::session::model) to the TTL
// configured for its provider; unknown providers return 0 (cache default).
// ttls must already be normalized.
func providerTTLResolver(ttls map[string]time.Duration) func(cacheKey string) time.Duration {
	if len(ttls) == 0 {
		return nil
	}
	return func(cacheKey string) time.Duration {
		provider, _, ok := strings.Cut(cacheKey, "::")
		if !ok {
			return 0
		}
		return ttls[strings.ToLower(provider)]
	}
}
