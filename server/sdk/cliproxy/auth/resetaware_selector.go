package auth

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

const (
	defaultResetAwareUrgencyWindow = 24 * time.Hour
	defaultResetAwareStaleAfter    = 15 * time.Minute
)

// cooldownNow supplies the clock for cooldown message rendering so tests can pin it.
var cooldownNow = time.Now

// cooldownProviderDisplayName returns the friendly provider name for providers
// that use the all-limited message, or "" for every other provider.
func cooldownProviderDisplayName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	default:
		return ""
	}
}

// formatCooldownClock renders t in host local time as "3:04 PM", or
// "Mon 3:04 PM" when t does not fall on the same local day as now.
func formatCooldownClock(t, now time.Time) string {
	t = t.Local()
	now = now.Local()
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("3:04 PM")
	}
	return t.Format("Mon 3:04 PM")
}

// friendlyMessage renders the all-limited message for Claude and Codex, or
// returns "" when the provider keeps the upstream message. With a zero freeAt
// (the upstream cooldown path) the time is now + resetIn and the account
// parenthetical is dropped.
func (e *modelCooldownError) friendlyMessage() string {
	displayName := cooldownProviderDisplayName(e.provider)
	if displayName == "" {
		return ""
	}
	now := e.now
	if now.IsZero() {
		now = cooldownNow()
	}
	freeAt := e.freeAt
	parenthetical := ""
	if freeAt.IsZero() {
		freeAt = now.Add(e.resetIn)
	} else if e.account != "" {
		if e.window != "" {
			parenthetical = fmt.Sprintf(" (%s, %s limit)", e.account, e.window)
		} else {
			parenthetical = fmt.Sprintf(" (%s)", e.account)
		}
	}
	return fmt.Sprintf("All %s accounts are limited. Earliest frees at %s%s.", displayName, formatCooldownClock(freeAt, now), parenthetical)
}

// ResetAwareConfig configures a ResetAwareSelector.
type ResetAwareConfig struct {
	// Store supplies quota snapshots; nil uses DefaultQuotaSnapshots().
	Store *QuotaSnapshotStore
	// UrgencyWindow is how soon a weekly reset must be for the rank-1 account
	// to count as urgent. Default 24h.
	UrgencyWindow time.Duration
	// StaleAfter is how old a snapshot may be before the account is treated as
	// unknown. Default 15m.
	StaleAfter time.Duration
	// Now supplies the clock. Default time.Now.
	Now func() time.Time
}

// RankedAuth is one account's position in the reset-aware ranking.
type RankedAuth struct {
	Auth *Auth
	// Usable is false when a relevant window is at or over its limit.
	Usable bool
	// Rank is 1-based among usable accounts for this provider+model; 0 when !Usable.
	Rank int
	// Known is true when a snapshot is present and not stale.
	Known bool
	// WeeklyResetAt is the ranking key: the earliest future reset among relevant
	// weekly windows with room left. Zero if unknown.
	WeeklyResetAt time.Time
	// Headroom is 100 - max utilization over relevant weekly windows.
	Headroom float64
	// LimitedBy is the label of the blocking window, e.g. "5-hour"; "" when usable.
	LimitedBy string
	// FreeAt is when the blocking window resets; zero when usable or unknown.
	FreeAt time.Time
	// Urgent is true for the rank-1 account when it is Known, its WeeklyResetAt
	// falls within the urgency window and it still has Headroom.
	Urgent bool
}

// ResetAwareSelector picks the usable account whose weekly quota resets soonest,
// using the snapshots a poller keeps in a QuotaSnapshotStore. It is meant to be
// the fallback inside a SessionAffinitySelector.
type ResetAwareSelector struct {
	store         *QuotaSnapshotStore
	urgencyWindow time.Duration
	staleAfter    time.Duration
	now           func() time.Time
}

// NewResetAwareSelector builds a selector, filling defaults for zero fields.
func NewResetAwareSelector(cfg ResetAwareConfig) *ResetAwareSelector {
	s := &ResetAwareSelector{
		store:         cfg.Store,
		urgencyWindow: cfg.UrgencyWindow,
		staleAfter:    cfg.StaleAfter,
		now:           cfg.Now,
	}
	if s.store == nil {
		s.store = DefaultQuotaSnapshots()
	}
	if s.urgencyWindow <= 0 {
		s.urgencyWindow = defaultResetAwareUrgencyWindow
	}
	if s.staleAfter <= 0 {
		s.staleAfter = defaultResetAwareStaleAfter
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// Pick returns the rank-1 usable account. The candidates have already been
// filtered by upstream for cooldowns and availability. When no candidate is
// usable it returns an all-limited *modelCooldownError (429) naming the
// account that frees earliest.
//
// Availability is checked across every priority tier: a lower-priority
// account must still be picked while every higher-priority one is
// snapshot-limited. Priority is applied as a tie-break inside the ranking.
func (s *ResetAwareSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	_ = opts
	now := s.now()
	available, err := getSelectorAvailableAuthsAcrossPriorities(ctx, auths, provider, model, now)
	if err != nil {
		return nil, err
	}
	available = preferCodexWebsocketAuths(ctx, provider, available)
	ranked := s.rankAt(model, available, now)
	if len(ranked) == 0 {
		return nil, &Error{Code: "auth_not_found", Message: "no auth candidates"}
	}
	if ranked[0].Usable {
		return ranked[0].Auth, nil
	}
	// Unusable entries are ordered by FreeAt, so the first one frees earliest.
	first := ranked[0]
	return nil, newAllLimitedErrorAt(provider, model, first.FreeAt, authDisplayLabel(first.Auth), first.LimitedBy, now)
}

// Rank orders auths for provider+model: usable accounts first by rank, then
// unusable accounts by FreeAt. It does not apply upstream cooldown state; pass
// candidates already filtered for availability when that matters.
func (s *ResetAwareSelector) Rank(provider, model string, auths []*Auth) []RankedAuth {
	_ = provider
	return s.rankAt(model, auths, s.now())
}

// Urgent returns the rank-1 usable account when its weekly window resets within
// the urgency window and it still has headroom. Unknown accounts are never urgent.
func (s *ResetAwareSelector) Urgent(provider, model string, auths []*Auth) (*Auth, bool) {
	ranked := s.Rank(provider, model, auths)
	if len(ranked) == 0 || !ranked[0].Urgent {
		return nil, false
	}
	return ranked[0].Auth, true
}

// NewAllLimitedError builds the 429 model_cooldown error returned when every
// account of a provider is limited. freeAt is when the earliest account frees;
// account and window name that account and its blocking window.
func NewAllLimitedError(provider, model string, freeAt time.Time, account, window string) error {
	return newAllLimitedErrorAt(provider, model, freeAt, account, window, cooldownNow())
}

func newAllLimitedErrorAt(provider, model string, freeAt time.Time, account, window string, now time.Time) *modelCooldownError {
	resetIn := time.Duration(0)
	if !freeAt.IsZero() {
		resetIn = freeAt.Sub(now)
	}
	err := newModelCooldownError(model, provider, resetIn)
	err.freeAt = freeAt
	err.account = account
	err.window = window
	err.now = now
	return err
}

func authDisplayLabel(auth *Auth) string {
	if auth == nil {
		return ""
	}
	if label := strings.TrimSpace(auth.Label); label != "" {
		return label
	}
	return auth.ID
}

func (s *ResetAwareSelector) rankAt(model string, auths []*Auth, now time.Time) []RankedAuth {
	ranked := make([]RankedAuth, 0, len(auths))
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		ranked = append(ranked, s.evaluate(auth, model, now))
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		return lessRanked(ranked[i], ranked[j])
	})
	rank := 0
	for i := range ranked {
		if !ranked[i].Usable {
			continue
		}
		rank++
		ranked[i].Rank = rank
	}
	if len(ranked) > 0 {
		first := &ranked[0]
		first.Urgent = first.Usable && first.Known && first.Headroom > 0 &&
			!first.WeeklyResetAt.IsZero() && first.WeeklyResetAt.After(now) &&
			first.WeeklyResetAt.Sub(now) <= s.urgencyWindow
	}
	return ranked
}

// relevantQuotaWindow reports whether window takes part in routing decisions for model.
func relevantQuotaWindow(window QuotaWindow, model string) bool {
	if !window.Routing {
		return false
	}
	if window.ModelScope == "" {
		return true
	}
	return strings.Contains(strings.ToLower(model), window.ModelScope)
}

// effectiveUtilization treats a window whose reset time has passed as fresh.
func effectiveUtilization(window QuotaWindow, now time.Time) float64 {
	if !window.ResetsAt.IsZero() && !window.ResetsAt.After(now) {
		return 0
	}
	return window.Utilization
}

// evaluate derives one account's ranking facts from its snapshot.
//
// A missing or stale snapshot makes the account unknown and usable, except that
// a blocking window whose ResetsAt is still in the future keeps its verdict:
// the limit is known to last at least until then. Stale windows still supply
// the weekly key and headroom so unknown accounts keep a sensible order among
// themselves; Known accounts always rank ahead of them.
func (s *ResetAwareSelector) evaluate(auth *Auth, model string, now time.Time) RankedAuth {
	out := RankedAuth{Auth: auth, Usable: true, Headroom: 100}
	snap, ok := s.store.Get(auth.ID)
	if !ok {
		return out
	}
	out.Known = !snap.Stale(now, s.staleAfter)

	var (
		maxWeekly      float64
		blockingWindow QuotaWindow
		blocking       bool
	)
	for _, window := range snap.Windows {
		if !relevantQuotaWindow(window, model) {
			continue
		}
		utilization := effectiveUtilization(window, now)
		if utilization >= 100 && (out.Known || window.ResetsAt.After(now)) {
			if !blocking || laterReset(window.ResetsAt, blockingWindow.ResetsAt) {
				blockingWindow = window
			}
			blocking = true
		}
		if !window.Weekly {
			continue
		}
		if utilization > maxWeekly {
			maxWeekly = utilization
		}
		// A past reset means the window is fresh but its next reset is unknown,
		// so it contributes no ranking key.
		if utilization < 100 && window.ResetsAt.After(now) {
			if out.WeeklyResetAt.IsZero() || window.ResetsAt.Before(out.WeeklyResetAt) {
				out.WeeklyResetAt = window.ResetsAt
			}
		}
	}
	out.Headroom = 100 - maxWeekly
	if blocking {
		out.Usable = false
		out.LimitedBy = blockingWindow.Label
		out.FreeAt = blockingWindow.ResetsAt
	}
	return out
}

// laterReset reports whether candidate should replace current as the window
// that frees the account: the account is free only once every blocking window
// has reset, so the latest known reset wins and a known reset beats an unknown one.
func laterReset(candidate, current time.Time) bool {
	if candidate.IsZero() {
		return false
	}
	if current.IsZero() {
		return true
	}
	return candidate.After(current)
}

// lessRanked orders usable accounts first (Known, then earliest weekly reset
// with zero last, then headroom, then priority, then ID) and unusable accounts
// by FreeAt with zero last.
func lessRanked(a, b RankedAuth) bool {
	if a.Usable != b.Usable {
		return a.Usable
	}
	if !a.Usable {
		if !a.FreeAt.Equal(b.FreeAt) {
			return earlierNonZero(a.FreeAt, b.FreeAt)
		}
		return a.Auth.ID < b.Auth.ID
	}
	if a.Known != b.Known {
		return a.Known
	}
	if !a.WeeklyResetAt.Equal(b.WeeklyResetAt) {
		return earlierNonZero(a.WeeklyResetAt, b.WeeklyResetAt)
	}
	if a.Headroom != b.Headroom {
		return a.Headroom > b.Headroom
	}
	if pa, pb := authPriority(a.Auth), authPriority(b.Auth); pa != pb {
		return pa > pb
	}
	return a.Auth.ID < b.Auth.ID
}

// earlierNonZero reports whether a sorts before b when earlier times come
// first and zero times come last. Callers guarantee a != b.
func earlierNonZero(a, b time.Time) bool {
	if a.IsZero() {
		return false
	}
	if b.IsZero() {
		return true
	}
	return a.Before(b)
}
