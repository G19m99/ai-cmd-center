package auth

import (
	"sync"
	"time"
)

// quotaNow supplies the current time to the quota parsers so tests can pin
// relative values such as Codex reset_after_seconds.
var quotaNow = time.Now

// QuotaWindow is one usage window of a provider account, normalized across
// Claude and Codex payloads.
type QuotaWindow struct {
	// Key is the provider key, e.g. "five_hour", "seven_day", "seven_day_opus",
	// "primary_window".
	Key string
	// Label is a short display name: "5-hour", "7-day", "7-day Opus", "Weekly".
	Label string
	// Utilization is the percent used, 0..100.
	Utilization float64
	// ResetsAt is when the window resets; zero if unknown.
	ResetsAt time.Time
	// Weekly is true when the window length is >= 6 days.
	Weekly bool
	// ModelScope is "" when the window applies to all models; otherwise a
	// lowercase substring matched against the model name ("opus", "sonnet",
	// "fable", or a Codex additional-limit name).
	ModelScope string
	// Routing is false for display-only windows (seven_day_oauth_apps,
	// seven_day_cowork, code review).
	Routing bool
}

// BonusResets describes the reset grants / reset credits an account can spend
// to clear a limited window early.
type BonusResets struct {
	// Known is false when the credits/grants call failed or was absent.
	Known     bool
	Available int
	// Clears lists the windows a grant clears, e.g. ["seven_day"] / ["five_hour"].
	Clears []string
	// ExpiresAt is the earliest expiry among available grants, zero if none.
	ExpiresAt time.Time
	Error     string
}

// QuotaSnapshot is the last known quota state of one auth entry.
type QuotaSnapshot struct {
	AuthID   string
	Provider string // "claude" | "codex"
	Windows  []QuotaWindow
	Bonus    BonusResets
	// FetchedAt is the time of the last successful fetch.
	FetchedAt   time.Time
	LastError   string
	LastErrorAt time.Time
}

// Stale reports whether the snapshot has never been fetched or its last
// successful fetch is older than staleAfter.
func (s QuotaSnapshot) Stale(now time.Time, staleAfter time.Duration) bool {
	if s.FetchedAt.IsZero() {
		return true
	}
	return now.Sub(s.FetchedAt) > staleAfter
}

// clone returns a deep copy so callers can never alias the stored slices.
func (s QuotaSnapshot) clone() QuotaSnapshot {
	out := s
	if s.Windows != nil {
		out.Windows = make([]QuotaWindow, len(s.Windows))
		copy(out.Windows, s.Windows)
	}
	if s.Bonus.Clears != nil {
		out.Bonus.Clears = make([]string, len(s.Bonus.Clears))
		copy(out.Bonus.Clears, s.Bonus.Clears)
	}
	return out
}

// QuotaSnapshotStore is an in-memory, concurrency-safe map of quota snapshots
// keyed by auth ID.
type QuotaSnapshotStore struct {
	mu    sync.RWMutex
	snaps map[string]QuotaSnapshot
}

// NewQuotaSnapshotStore returns an empty store.
func NewQuotaSnapshotStore() *QuotaSnapshotStore {
	return &QuotaSnapshotStore{snaps: make(map[string]QuotaSnapshot)}
}

var (
	defaultQuotaSnapshotsOnce sync.Once
	defaultQuotaSnapshots     *QuotaSnapshotStore
)

// DefaultQuotaSnapshots returns the process-wide store. Selectors are rebuilt
// on config reload; the store is not, so polled snapshots survive reloads.
func DefaultQuotaSnapshots() *QuotaSnapshotStore {
	defaultQuotaSnapshotsOnce.Do(func() {
		defaultQuotaSnapshots = NewQuotaSnapshotStore()
	})
	return defaultQuotaSnapshots
}

// Get returns a deep copy of the snapshot for authID.
func (s *QuotaSnapshotStore) Get(authID string) (QuotaSnapshot, bool) {
	if s == nil {
		return QuotaSnapshot{}, false
	}
	s.mu.RLock()
	snap, ok := s.snaps[authID]
	s.mu.RUnlock()
	if !ok {
		return QuotaSnapshot{}, false
	}
	return snap.clone(), true
}

// Put replaces the snapshot for snap.AuthID with a deep copy of snap.
func (s *QuotaSnapshotStore) Put(snap QuotaSnapshot) {
	if s == nil || snap.AuthID == "" {
		return
	}
	stored := snap.clone()
	s.mu.Lock()
	s.snaps[snap.AuthID] = stored
	s.mu.Unlock()
}

// MarkFailed records a fetch failure while keeping the previous Windows, Bonus
// and FetchedAt. An unknown authID gets a snapshot with zero FetchedAt so it
// reads as stale.
func (s *QuotaSnapshotStore) MarkFailed(authID, provider string, err error, at time.Time) {
	if s == nil || authID == "" {
		return
	}
	message := ""
	if err != nil {
		message = err.Error()
	}
	if at.IsZero() {
		at = quotaNow()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snaps[authID]
	if !ok {
		snap = QuotaSnapshot{AuthID: authID, Provider: provider}
	}
	if snap.Provider == "" {
		snap.Provider = provider
	}
	snap.LastError = message
	snap.LastErrorAt = at
	s.snaps[authID] = snap
}

// Delete removes the snapshot for authID.
func (s *QuotaSnapshotStore) Delete(authID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.snaps, authID)
	s.mu.Unlock()
}
