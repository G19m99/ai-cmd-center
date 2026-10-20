package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// shiftSessionCacheExpiry moves every binding's expiry d into the past, which
// is equivalent to the session being idle for d without touching wall-clock time.
func shiftSessionCacheExpiry(c *SessionCache, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range c.entries {
		entry.expiresAt = entry.expiresAt.Add(-d)
		c.entries[key] = entry
	}
	for key, group := range c.groups {
		group.expiresAt = group.expiresAt.Add(-d)
		c.groups[key] = group
	}
}

func TestSessionCache_ProviderTTL(t *testing.T) {
	t.Parallel()

	cache := NewSessionCache(time.Hour)
	defer cache.Stop()
	cache.SetTTLResolver(func(cacheKey string) time.Duration {
		switch provider, _, _ := cutProviderPrefix(cacheKey); provider {
		case "claude":
			return time.Hour
		case "codex":
			return 10 * time.Minute
		default:
			return 0
		}
	})

	cache.Set("codex::sess-1::gpt-5", "codex-b")
	cache.Set("claude::sess-1::claude-sonnet-5-5", "claude-b")

	shiftSessionCacheExpiry(cache, 11*time.Minute)

	if got, ok := cache.Get("codex::sess-1::gpt-5"); ok {
		t.Fatalf("codex binding after 11m idle = %q, want expired", got)
	}
	if got, ok := cache.GetAndRefresh("claude::sess-1::claude-sonnet-5-5"); !ok || got != "claude-b" {
		t.Fatalf("claude binding after 11m idle = %q, %v; want claude-b, true", got, ok)
	}

	// GetAndRefresh renewed the claude binding for a full hour, so another 50m idle keeps it.
	shiftSessionCacheExpiry(cache, 50*time.Minute)
	if got, ok := cache.Get("claude::sess-1::claude-sonnet-5-5"); !ok || got != "claude-b" {
		t.Fatalf("claude binding after refresh and 50m idle = %q, %v; want claude-b, true", got, ok)
	}

	// Unknown providers fall back to the default TTL.
	cache.Set("gemini::sess-1::gemini-2.5-pro", "gemini-a")
	shiftSessionCacheExpiry(cache, 30*time.Minute)
	if got, ok := cache.Get("gemini::sess-1::gemini-2.5-pro"); !ok || got != "gemini-a" {
		t.Fatalf("default-TTL binding after 30m idle = %q, %v; want gemini-a, true", got, ok)
	}
}

func cutProviderPrefix(cacheKey string) (string, string, bool) {
	for i := 0; i+1 < len(cacheKey); i++ {
		if cacheKey[i] == ':' && cacheKey[i+1] == ':' {
			return cacheKey[:i], cacheKey[i+2:], true
		}
	}
	return "", cacheKey, false
}

func TestSessionCache_CountByAuth(t *testing.T) {
	t.Parallel()

	cache := NewSessionCache(time.Hour)
	defer cache.Stop()

	cache.SetAliases("A", "claude::sess-1::m", "claude::parent-1::m")
	cache.Set("claude::sess-2::m", "A")
	cache.Set("claude::sess-3::m", "B")

	got := cache.CountByAuth()
	if got["A"] != 2 || got["B"] != 1 || len(got) != 2 {
		t.Fatalf("CountByAuth() = %v, want {A:2 B:1}", got)
	}

	// One conversation calling a second model (Claude Code also calling haiku)
	// is still one session: the model suffix of the cache key is ignored.
	cache.Set("claude::sess-3::haiku", "B")
	cache.Set("claude::sess-2::haiku", "A")
	got = cache.CountByAuth()
	if got["A"] != 2 || got["B"] != 1 || len(got) != 2 {
		t.Fatalf("CountByAuth() with a second model per session = %v, want {A:2 B:1}", got)
	}

	// A conversation bound to a different auth for another model counts for
	// each auth it is bound to.
	cache.Set("claude::sess-3::opus", "A")
	got = cache.CountByAuth()
	if got["A"] != 3 || got["B"] != 1 {
		t.Fatalf("CountByAuth() with a session split across auths = %v, want {A:3 B:1}", got)
	}
	cache.Set("claude::sess-3::opus", "B")

	// Expired groups are not counted.
	cache.Set("claude::sess-4::m", "C")
	shiftSessionCacheExpiry(cache, 2*time.Hour)
	cache.Set("claude::sess-5::m", "B")
	got = cache.CountByAuth()
	if got["A"] != 0 || got["B"] != 1 || got["C"] != 0 || len(got) != 1 {
		t.Fatalf("CountByAuth() after expiry = %v, want {B:1}", got)
	}
}

// affinityFixture wraps a ResetAwareSelector in a SessionAffinitySelector with
// per-provider TTLs and a private snapshot store.
type affinityFixture struct {
	store    *QuotaSnapshotStore
	now      time.Time
	reset    *ResetAwareSelector
	affinity *SessionAffinitySelector
}

func newAffinityFixture(t *testing.T) *affinityFixture {
	t.Helper()
	f := &affinityFixture{
		store: NewQuotaSnapshotStore(),
		now:   time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local),
	}
	f.reset = NewResetAwareSelector(ResetAwareConfig{
		Store: f.store,
		Now:   func() time.Time { return f.now },
	})
	f.affinity = NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{
		Fallback: f.reset,
		TTL:      time.Hour,
		ProviderTTL: map[string]time.Duration{
			"claude": time.Hour,
			"codex":  10 * time.Minute,
		},
	})
	t.Cleanup(f.affinity.Stop)
	return f
}

func (f *affinityFixture) put(authID, provider string, windows ...QuotaWindow) {
	f.store.Put(QuotaSnapshot{AuthID: authID, Provider: provider, Windows: windows, FetchedAt: f.now})
}

func (f *affinityFixture) weeklyIn(authID, provider string, in time.Duration, utilization float64) {
	f.put(authID, provider, sevenDayWindow(utilization, f.now.Add(in)))
}

func sessionOpts(sessionID string) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{Metadata: map[string]any{
		cliproxyexecutor.DerivedSessionIDMetadataKey: sessionID,
	}}
}

func (f *affinityFixture) pick(t *testing.T, label, provider, model, sessionID string, auths []*Auth) *Auth {
	t.Helper()
	got, err := f.affinity.Pick(context.Background(), provider, model, sessionOpts(sessionID), auths)
	if err != nil {
		t.Fatalf("%s: Pick() error = %v", label, err)
	}
	if got == nil {
		t.Fatalf("%s: Pick() = nil", label)
	}
	return got
}

func (f *affinityFixture) expect(t *testing.T, label, provider, model, sessionID string, auths []*Auth, wantID string) {
	t.Helper()
	if got := f.pick(t, label, provider, model, sessionID, auths); got.ID != wantID {
		t.Fatalf("%s: Pick() = %q, want %q", label, got.ID, wantID)
	}
}

const (
	affinityClaudeModel = "claude-sonnet-5-5"
	affinityCodexModel  = "gpt-5"
)

// bindToB makes B rank 1 and binds the session to it, then makes A rank 1 but
// not urgent (resets in 3 days, B in 4 days).
func (f *affinityFixture) bindToB(t *testing.T, provider, model, sessionID string, auths []*Auth) {
	t.Helper()
	f.weeklyIn("A", provider, 5*24*time.Hour, 50)
	f.weeklyIn("B", provider, 2*24*time.Hour, 50)
	f.expect(t, "cold binding", provider, model, sessionID, auths, "B")

	f.weeklyIn("A", provider, 3*24*time.Hour, 50)
	f.weeklyIn("B", provider, 4*24*time.Hour, 50)
	if ranked := f.reset.Rank(provider, model, auths); ranked[0].Auth.ID != "A" || ranked[0].Urgent {
		t.Fatalf("precondition: Rank()[0] = %+v, want A non-urgent", ranked[0])
	}
}

func TestAffinity_StaysOnBoundWhileActive(t *testing.T) {
	f := newAffinityFixture(t)
	auths := []*Auth{claudeAuth("A"), claudeAuth("B")}
	f.bindToB(t, "claude", affinityClaudeModel, "sess-1", auths)

	f.expect(t, "active session", "claude", affinityClaudeModel, "sess-1", auths, "B")
	f.expect(t, "active session again", "claude", affinityClaudeModel, "sess-1", auths, "B")
}

func TestAffinity_ReroutesAfterProviderIdleTTL(t *testing.T) {
	t.Run("codex rerouted after 11m", func(t *testing.T) {
		f := newAffinityFixture(t)
		auths := []*Auth{{ID: "A", Provider: "codex"}, {ID: "B", Provider: "codex"}}
		f.bindToB(t, "codex", affinityCodexModel, "sess-1", auths)

		shiftSessionCacheExpiry(f.affinity.cache, 11*time.Minute)
		f.expect(t, "codex after idle", "codex", affinityCodexModel, "sess-1", auths, "A")
		f.expect(t, "codex sticky on new binding", "codex", affinityCodexModel, "sess-1", auths, "A")
	})

	t.Run("claude stays after 11m", func(t *testing.T) {
		f := newAffinityFixture(t)
		auths := []*Auth{claudeAuth("A"), claudeAuth("B")}
		f.bindToB(t, "claude", affinityClaudeModel, "sess-1", auths)

		shiftSessionCacheExpiry(f.affinity.cache, 11*time.Minute)
		f.expect(t, "claude after idle", "claude", affinityClaudeModel, "sess-1", auths, "B")
	})
}

func TestAffinity_UrgencyRebindsOnNextRequest(t *testing.T) {
	f := newAffinityFixture(t)
	auths := []*Auth{claudeAuth("A"), claudeAuth("B")}
	f.bindToB(t, "claude", affinityClaudeModel, "sess-1", auths)

	f.weeklyIn("A", "claude", 20*time.Hour, 50)
	if urgent, ok := f.reset.Urgent("claude", affinityClaudeModel, auths); !ok || urgent.ID != "A" {
		t.Fatalf("precondition: Urgent() = %v, %v; want A", urgent, ok)
	}
	f.expect(t, "urgent rebind", "claude", affinityClaudeModel, "sess-1", auths, "A")

	// The binding moved: once A is no longer urgent the session still sticks to A.
	f.weeklyIn("A", "claude", 3*24*time.Hour, 50)
	f.expect(t, "sticky after rebind", "claude", affinityClaudeModel, "sess-1", auths, "A")

	counts := f.affinity.BindingCounts()
	if counts["A"] != 1 || counts["B"] != 0 {
		t.Fatalf("BindingCounts() = %v, want {A:1}", counts)
	}
}

func TestAffinity_UrgencyIgnoresUnusableUrgent(t *testing.T) {
	t.Run("five-hour window full", func(t *testing.T) {
		f := newAffinityFixture(t)
		auths := []*Auth{claudeAuth("A"), claudeAuth("B")}
		f.bindToB(t, "claude", affinityClaudeModel, "sess-1", auths)

		f.put("A", "claude",
			fiveHourWindow(100, f.now.Add(2*time.Hour)),
			sevenDayWindow(50, f.now.Add(20*time.Hour)))
		f.expect(t, "urgent but limited", "claude", affinityClaudeModel, "sess-1", auths, "B")
	})

	t.Run("cannot serve the model", func(t *testing.T) {
		f := newAffinityFixture(t)
		auths := []*Auth{claudeAuth("A"), claudeAuth("B")}
		f.bindToB(t, "claude", affinityClaudeModel, "sess-1", auths)

		f.weeklyIn("A", "claude", 20*time.Hour, 50)
		blockedA := &Auth{ID: "A", Provider: "claude", ModelStates: map[string]*ModelState{
			affinityClaudeModel: {Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour)},
		}}
		f.expect(t, "urgent but model blocked", "claude", affinityClaudeModel, "sess-1", []*Auth{blockedA, claudeAuth("B")}, "B")
	})

	t.Run("already bound to the urgent auth", func(t *testing.T) {
		f := newAffinityFixture(t)
		auths := []*Auth{claudeAuth("A"), claudeAuth("B")}
		f.weeklyIn("A", "claude", 20*time.Hour, 50)
		f.weeklyIn("B", "claude", 4*24*time.Hour, 50)

		f.expect(t, "cold binding to urgent", "claude", affinityClaudeModel, "sess-2", auths, "A")
		f.expect(t, "stays on urgent", "claude", affinityClaudeModel, "sess-2", auths, "A")
		if counts := f.affinity.BindingCounts(); counts["A"] != 1 {
			t.Fatalf("BindingCounts() = %v, want {A:1}", counts)
		}
	})
}

func TestAffinity_MovesWhenBoundLimited(t *testing.T) {
	f := newAffinityFixture(t)
	auths := []*Auth{claudeAuth("A"), claudeAuth("B")}
	f.bindToB(t, "claude", affinityClaudeModel, "sess-1", auths)

	limitedB := &Auth{ID: "B", Provider: "claude", Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour)}
	f.expect(t, "bound auth limited", "claude", affinityClaudeModel, "sess-1", []*Auth{claudeAuth("A"), limitedB}, "A")

	// B recovers, but the session was rebound to A.
	f.expect(t, "sticky after failover", "claude", affinityClaudeModel, "sess-1", auths, "A")
}

func TestAffinity_ResetAwareFallbackSeesAllPriorityTiers(t *testing.T) {
	f := newAffinityFixture(t)
	high := &Auth{ID: "high", Provider: "claude", Attributes: map[string]string{"priority": "5"}}
	low := &Auth{ID: "low", Provider: "claude"}
	auths := []*Auth{high, low}

	f.put("high", "claude",
		fiveHourWindow(100, f.now.Add(2*time.Hour)),
		sevenDayWindow(50, f.now.Add(24*time.Hour)))
	f.weeklyIn("low", "claude", 3*24*time.Hour, 50)

	f.expect(t, "cold binding falls through to lower tier", "claude", affinityClaudeModel, "sess-1", auths, "low")

	got, err := f.affinity.Pick(context.Background(), "claude", affinityClaudeModel, cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("no-session Pick() error = %v", err)
	}
	if got == nil || got.ID != "low" {
		t.Fatalf("no-session Pick() = %v, want low", got)
	}

	// Once the high-priority account frees, a new session binds to it.
	f.weeklyIn("high", "claude", 24*time.Hour, 50)
	f.expect(t, "high-priority account usable again", "claude", affinityClaudeModel, "sess-2", auths, "high")
}

func TestAffinity_OtherFallbacksKeepHighestTierOnly(t *testing.T) {
	selector := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{
		Fallback: lastAuthSelector{},
		TTL:      time.Hour,
	})
	defer selector.Stop()

	high := &Auth{ID: "a-high", Provider: "test", Status: StatusActive, Attributes: map[string]string{"priority": "1"}}
	low := &Auth{ID: "z-low", Provider: "test", Status: StatusActive, Attributes: map[string]string{"priority": "0"}}
	got, err := selector.Pick(context.Background(), "test", "model", sessionOpts("sess-1"), []*Auth{high, low})
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got == nil || got.ID != high.ID {
		t.Fatalf("Pick() = %v, want %q", got, high.ID)
	}
	if fallback := selector.Fallback(); fallback == nil {
		t.Fatal("Fallback() = nil")
	} else if _, ok := fallback.(lastAuthSelector); !ok {
		t.Fatalf("Fallback() = %T, want lastAuthSelector", fallback)
	}
}

func TestSessionCache_CountByAuth_SameSessionTwoModels(t *testing.T) {
	t.Parallel()

	cache := NewSessionCache(time.Hour)
	defer cache.Stop()

	cache.Set("claude::sess-1::claude-sonnet-5-5", "A")
	cache.Set("claude::sess-1::claude-haiku-4-5", "A")

	if got := cache.CountByAuth(); got["A"] != 1 || len(got) != 1 {
		t.Fatalf("CountByAuth() same session on two models = %v, want {A:1}", got)
	}

	// Keys without a model segment count as their own conversation.
	cache.Set("claude::sess-2", "A")
	if got := cache.CountByAuth(); got["A"] != 2 {
		t.Fatalf("CountByAuth() with a model-less key = %v, want {A:2}", got)
	}
	for key, want := range map[string]string{
		"claude::sess-1::claude-sonnet-5-5": "claude::sess-1",
		"claude::sess-2":                    "claude::sess-2",
		"sess-3":                            "sess-3",
	} {
		if got := sessionConversationKey(key); got != want {
			t.Fatalf("sessionConversationKey(%q) = %q, want %q", key, got, want)
		}
	}
}
