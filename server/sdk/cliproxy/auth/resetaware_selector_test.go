package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// resetAwareFixture wires a selector to a private store and a pinned clock.
type resetAwareFixture struct {
	store    *QuotaSnapshotStore
	now      time.Time
	selector *ResetAwareSelector
}

func newResetAwareFixture(t *testing.T, now time.Time) *resetAwareFixture {
	t.Helper()
	f := &resetAwareFixture{store: NewQuotaSnapshotStore(), now: now}
	f.selector = NewResetAwareSelector(ResetAwareConfig{
		Store: f.store,
		Now:   func() time.Time { return f.now },
	})
	return f
}

// put stores a fresh snapshot for authID made of the given windows.
func (f *resetAwareFixture) put(authID, provider string, windows ...QuotaWindow) {
	f.store.Put(QuotaSnapshot{
		AuthID:    authID,
		Provider:  provider,
		Windows:   windows,
		FetchedAt: f.now,
	})
}

func fiveHourWindow(utilization float64, resetsAt time.Time) QuotaWindow {
	return QuotaWindow{Key: "five_hour", Label: "5-hour", Utilization: utilization, ResetsAt: resetsAt, Routing: true}
}

func sevenDayWindow(utilization float64, resetsAt time.Time) QuotaWindow {
	return QuotaWindow{Key: "seven_day", Label: "7-day", Utilization: utilization, ResetsAt: resetsAt, Weekly: true, Routing: true}
}

func sevenDayOpusWindow(utilization float64, resetsAt time.Time) QuotaWindow {
	return QuotaWindow{Key: "seven_day_opus", Label: "7-day Opus", Utilization: utilization, ResetsAt: resetsAt, Weekly: true, ModelScope: "opus", Routing: true}
}

func claudeAuth(id string) *Auth {
	return &Auth{ID: id, Provider: "claude"}
}

func pinCooldownNow(t *testing.T, now time.Time) {
	t.Helper()
	previous := cooldownNow
	cooldownNow = func() time.Time { return now }
	t.Cleanup(func() { cooldownNow = previous })
}

func rankByID(t *testing.T, ranked []RankedAuth, id string) RankedAuth {
	t.Helper()
	for _, entry := range ranked {
		if entry.Auth != nil && entry.Auth.ID == id {
			return entry
		}
	}
	t.Fatalf("auth %q not in ranking %+v", id, ranked)
	return RankedAuth{}
}

func cooldownMessage(t *testing.T, err error) (string, map[string]any) {
	t.Helper()
	var payload map[string]any
	if errUnmarshal := json.Unmarshal([]byte(err.Error()), &payload); errUnmarshal != nil {
		t.Fatalf("json.Unmarshal(Error()) error = %v; raw=%s", errUnmarshal, err.Error())
	}
	rawErr, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("Error() payload missing error object: %v", payload)
	}
	message, _ := rawErr["message"].(string)
	return message, rawErr
}

func TestRank_SoonestWeeklyResetFirst(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	f := newResetAwareFixture(t, now)
	f.put("claude-a", "claude", sevenDayWindow(60, now.Add(24*time.Hour)))
	f.put("claude-b", "claude", sevenDayWindow(10, now.Add(4*24*time.Hour)))
	auths := []*Auth{claudeAuth("claude-b"), claudeAuth("claude-a")}

	picked, err := f.selector.Pick(context.Background(), "claude", "claude-sonnet-5-5", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if picked.ID != "claude-a" {
		t.Fatalf("Pick() = %q, want claude-a", picked.ID)
	}

	ranked := f.selector.Rank("claude", "claude-sonnet-5-5", auths)
	if len(ranked) != 2 {
		t.Fatalf("Rank() len = %d, want 2", len(ranked))
	}
	if ranked[0].Auth.ID != "claude-a" || ranked[0].Rank != 1 || !ranked[0].Usable || !ranked[0].Known {
		t.Fatalf("ranked[0] = %+v, want claude-a rank 1 usable known", ranked[0])
	}
	if ranked[1].Auth.ID != "claude-b" || ranked[1].Rank != 2 {
		t.Fatalf("ranked[1] = %+v, want claude-b rank 2", ranked[1])
	}
	if ranked[0].Headroom != 40 || ranked[1].Headroom != 90 {
		t.Fatalf("headroom = %v/%v, want 40/90", ranked[0].Headroom, ranked[1].Headroom)
	}
	if !ranked[0].WeeklyResetAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("WeeklyResetAt = %v, want %v", ranked[0].WeeklyResetAt, now.Add(24*time.Hour))
	}
}

func TestRank_FiveHourFullSpillsAndReturns(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	f := newResetAwareFixture(t, now)
	fiveHourReset := now.Add(2 * time.Hour)
	f.put("claude-a", "claude",
		fiveHourWindow(100, fiveHourReset),
		sevenDayWindow(60, now.Add(24*time.Hour)))
	f.put("claude-b", "claude",
		fiveHourWindow(20, now.Add(3*time.Hour)),
		sevenDayWindow(10, now.Add(4*24*time.Hour)))
	auths := []*Auth{claudeAuth("claude-a"), claudeAuth("claude-b")}

	picked, err := f.selector.Pick(context.Background(), "claude", "claude-sonnet-5-5", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if picked.ID != "claude-b" {
		t.Fatalf("Pick() = %q, want claude-b while A's 5-hour window is full", picked.ID)
	}

	ranked := f.selector.Rank("claude", "claude-sonnet-5-5", auths)
	a := rankByID(t, ranked, "claude-a")
	if a.Usable || a.Rank != 0 {
		t.Fatalf("claude-a = %+v, want unusable with rank 0", a)
	}
	if a.LimitedBy != "5-hour" {
		t.Fatalf("claude-a LimitedBy = %q, want 5-hour", a.LimitedBy)
	}
	if !a.FreeAt.Equal(fiveHourReset) {
		t.Fatalf("claude-a FreeAt = %v, want %v", a.FreeAt, fiveHourReset)
	}
	if ranked[0].Auth.ID != "claude-b" || ranked[1].Auth.ID != "claude-a" {
		t.Fatalf("Rank() order = %q,%q, want usable first", ranked[0].Auth.ID, ranked[1].Auth.ID)
	}

	// Snapshots are not refreshed; the window's reset time passes.
	f.now = now.Add(2*time.Hour + time.Minute)
	picked, err = f.selector.Pick(context.Background(), "claude", "claude-sonnet-5-5", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() after reset error = %v", err)
	}
	if picked.ID != "claude-a" {
		t.Fatalf("Pick() after reset = %q, want claude-a", picked.ID)
	}
}

func TestRank_ModelScopedWindowBlocksOnlyThatModel(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	f := newResetAwareFixture(t, now)
	f.put("claude-a", "claude",
		sevenDayWindow(40, now.Add(24*time.Hour)),
		sevenDayOpusWindow(100, now.Add(24*time.Hour)))
	f.put("claude-b", "claude", sevenDayWindow(10, now.Add(4*24*time.Hour)))
	auths := []*Auth{claudeAuth("claude-a"), claudeAuth("claude-b")}

	picked, err := f.selector.Pick(context.Background(), "claude", "claude-opus-5-5", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick(opus) error = %v", err)
	}
	if picked.ID != "claude-b" {
		t.Fatalf("Pick(opus) = %q, want claude-b", picked.ID)
	}
	a := rankByID(t, f.selector.Rank("claude", "claude-opus-5-5", auths), "claude-a")
	if a.Usable || a.LimitedBy != "7-day Opus" {
		t.Fatalf("claude-a for opus = %+v, want limited by 7-day Opus", a)
	}

	picked, err = f.selector.Pick(context.Background(), "claude", "claude-sonnet-5-5", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick(sonnet) error = %v", err)
	}
	if picked.ID != "claude-a" {
		t.Fatalf("Pick(sonnet) = %q, want claude-a", picked.ID)
	}
}

func TestRank_UnknownRankedLast(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	f := newResetAwareFixture(t, now)
	f.put("claude-b", "claude", sevenDayWindow(90, now.Add(6*24*time.Hour)))
	auths := []*Auth{claudeAuth("claude-a"), claudeAuth("claude-b")}

	picked, err := f.selector.Pick(context.Background(), "claude", "claude-sonnet-5-5", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if picked.ID != "claude-b" {
		t.Fatalf("Pick() = %q, want known claude-b before unknown claude-a", picked.ID)
	}
	ranked := f.selector.Rank("claude", "claude-sonnet-5-5", auths)
	a := rankByID(t, ranked, "claude-a")
	if !a.Usable || a.Known || a.Rank != 2 || !a.WeeklyResetAt.IsZero() {
		t.Fatalf("unknown claude-a = %+v, want usable, unknown, rank 2, zero weekly key", a)
	}

	picked, err = f.selector.Pick(context.Background(), "claude", "claude-sonnet-5-5", cliproxyexecutor.Options{}, []*Auth{claudeAuth("claude-a")})
	if err != nil {
		t.Fatalf("Pick(only unknown) error = %v", err)
	}
	if picked.ID != "claude-a" {
		t.Fatalf("Pick(only unknown) = %q, want claude-a", picked.ID)
	}
}

func TestRank_StaleSnapshotKeepsFutureLimitOnly(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	f := newResetAwareFixture(t, now)
	// Fetched 20m ago: stale under the 15m default.
	f.store.Put(QuotaSnapshot{
		AuthID:    "claude-a",
		Provider:  "claude",
		FetchedAt: now.Add(-20 * time.Minute),
		Windows: []QuotaWindow{
			fiveHourWindow(100, now.Add(time.Hour)),
			sevenDayWindow(30, now.Add(24*time.Hour)),
		},
	})
	f.store.Put(QuotaSnapshot{
		AuthID:    "claude-b",
		Provider:  "claude",
		FetchedAt: now.Add(-20 * time.Minute),
		Windows: []QuotaWindow{
			fiveHourWindow(100, time.Time{}),
			sevenDayWindow(30, now.Add(24*time.Hour)),
		},
	})
	ranked := f.selector.Rank("claude", "claude-sonnet-5-5", []*Auth{claudeAuth("claude-a"), claudeAuth("claude-b")})
	a := rankByID(t, ranked, "claude-a")
	if a.Usable || a.Known || a.LimitedBy != "5-hour" {
		t.Fatalf("stale claude-a with future reset = %+v, want unusable, unknown, limited by 5-hour", a)
	}
	b := rankByID(t, ranked, "claude-b")
	if !b.Usable || b.Known {
		t.Fatalf("stale claude-b without reset time = %+v, want usable and unknown", b)
	}
}

func TestRank_PastResetTreatedAsFresh(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	f := newResetAwareFixture(t, now)
	f.put("claude-a", "claude", sevenDayWindow(100, now.Add(-time.Minute)))
	auths := []*Auth{claudeAuth("claude-a")}

	ranked := f.selector.Rank("claude", "claude-sonnet-5-5", auths)
	a := rankByID(t, ranked, "claude-a")
	if !a.Usable || a.Rank != 1 || a.LimitedBy != "" || !a.FreeAt.IsZero() {
		t.Fatalf("claude-a = %+v, want usable rank 1", a)
	}
	if a.Headroom != 100 {
		t.Fatalf("Headroom = %v, want 100 after the reset passed", a.Headroom)
	}
	picked, err := f.selector.Pick(context.Background(), "claude", "claude-sonnet-5-5", cliproxyexecutor.Options{}, auths)
	if err != nil || picked.ID != "claude-a" {
		t.Fatalf("Pick() = %v, %v; want claude-a", picked, err)
	}
}

func TestRank_TieBreaksHeadroomThenPriorityThenID(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	f := newResetAwareFixture(t, now)
	reset := now.Add(24 * time.Hour)
	f.put("claude-a", "claude", sevenDayWindow(50, reset))
	f.put("claude-b", "claude", sevenDayWindow(30, reset))
	f.put("claude-c", "claude", sevenDayWindow(30, reset))
	f.put("claude-d", "claude", sevenDayWindow(30, reset))
	c := claudeAuth("claude-c")
	c.Attributes = map[string]string{"priority": "5"}
	ranked := f.selector.Rank("claude", "", []*Auth{claudeAuth("claude-d"), c, claudeAuth("claude-a"), claudeAuth("claude-b")})
	var order []string
	for _, entry := range ranked {
		order = append(order, entry.Auth.ID)
	}
	if got := strings.Join(order, ","); got != "claude-c,claude-b,claude-d,claude-a" {
		t.Fatalf("Rank() order = %s, want claude-c,claude-b,claude-d,claude-a", got)
	}
	for i, entry := range ranked {
		if entry.Rank != i+1 {
			t.Fatalf("ranked[%d].Rank = %d, want %d", i, entry.Rank, i+1)
		}
	}
}

func TestPick_LimitedHighPriorityFallsThroughToLowerTier(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	f := newResetAwareFixture(t, now)
	f.put("claude-high", "claude",
		fiveHourWindow(100, now.Add(2*time.Hour)),
		sevenDayWindow(60, now.Add(24*time.Hour)))
	f.put("claude-low", "claude",
		fiveHourWindow(10, now.Add(3*time.Hour)),
		sevenDayWindow(10, now.Add(4*24*time.Hour)))
	high := claudeAuth("claude-high")
	high.Attributes = map[string]string{"priority": "5"}
	low := claudeAuth("claude-low")
	auths := []*Auth{high, low}

	picked, err := f.selector.Pick(context.Background(), "claude", "claude-sonnet-5-5", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v, want the lower-priority usable account", err)
	}
	if picked.ID != "claude-low" {
		t.Fatalf("Pick() = %q, want claude-low", picked.ID)
	}

	// Once the high-priority account frees, priority breaks the tie only among
	// accounts with equal weekly keys; here the sooner weekly reset still wins.
	f.now = now.Add(2*time.Hour + time.Minute)
	picked, err = f.selector.Pick(context.Background(), "claude", "claude-sonnet-5-5", cliproxyexecutor.Options{}, auths)
	if err != nil || picked.ID != "claude-high" {
		t.Fatalf("Pick() after reset = %v, %v; want claude-high", picked, err)
	}
}

func TestUrgent_WithinWindowWithHeadroom(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	f := newResetAwareFixture(t, now)
	f.put("claude-a", "claude", sevenDayWindow(70, now.Add(20*time.Hour)))
	f.put("claude-b", "claude", sevenDayWindow(10, now.Add(4*24*time.Hour)))
	auths := []*Auth{claudeAuth("claude-a"), claudeAuth("claude-b")}

	urgent, ok := f.selector.Urgent("claude", "claude-sonnet-5-5", auths)
	if !ok || urgent == nil || urgent.ID != "claude-a" {
		t.Fatalf("Urgent() = %v, %v; want claude-a, true", urgent, ok)
	}
	ranked := f.selector.Rank("claude", "claude-sonnet-5-5", auths)
	if !ranked[0].Urgent || ranked[1].Urgent {
		t.Fatalf("Rank() urgent flags = %v/%v, want true/false", ranked[0].Urgent, ranked[1].Urgent)
	}

	f.put("claude-a", "claude", sevenDayWindow(70, now.Add(30*time.Hour)))
	if urgent, ok := f.selector.Urgent("claude", "claude-sonnet-5-5", auths); ok {
		t.Fatalf("Urgent() with reset in 30h = %v, true; want false", urgent)
	}

	f.put("claude-a", "claude", sevenDayWindow(100, now.Add(20*time.Hour)))
	if urgent, ok := f.selector.Urgent("claude", "claude-sonnet-5-5", auths); ok {
		t.Fatalf("Urgent() with 0%% headroom = %v, true; want false", urgent)
	}

	// Unknown accounts are never urgent, even when they rank first.
	f.store.Delete("claude-a")
	f.store.Delete("claude-b")
	if urgent, ok := f.selector.Urgent("claude", "claude-sonnet-5-5", auths); ok {
		t.Fatalf("Urgent() with unknown accounts = %v, true; want false", urgent)
	}
}

func TestPick_AllLimitedReturnsFriendlyError(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	f := newResetAwareFixture(t, now)
	f.put("claude-a", "claude",
		fiveHourWindow(100, now.Add(40*time.Minute)),
		sevenDayWindow(60, now.Add(24*time.Hour)))
	f.put("claude-b", "claude",
		fiveHourWindow(100, now.Add(3*time.Hour)),
		sevenDayWindow(10, now.Add(4*24*time.Hour)))
	auths := []*Auth{claudeAuth("claude-b"), claudeAuth("claude-a")}

	picked, err := f.selector.Pick(context.Background(), "claude", "claude-sonnet-5-5", cliproxyexecutor.Options{}, auths)
	if err == nil {
		t.Fatalf("Pick() = %v, want all-limited error", picked)
	}
	var mce *modelCooldownError
	if !errors.As(err, &mce) {
		t.Fatalf("Pick() error = %T (%v), want *modelCooldownError", err, err)
	}
	if mce.StatusCode() != http.StatusTooManyRequests {
		t.Fatalf("StatusCode() = %d, want 429", mce.StatusCode())
	}
	if got := mce.Headers().Get("Retry-After"); got != "2400" {
		t.Fatalf("Retry-After = %q, want 2400", got)
	}
	message, rawErr := cooldownMessage(t, err)
	want := "All Claude accounts are limited. Earliest frees at 3:40 PM (claude-a, 5-hour limit)."
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
	if got, _ := rawErr["reset_seconds"].(float64); got != 2400 {
		t.Fatalf("reset_seconds = %v, want 2400", got)
	}
	if got, _ := rawErr["code"].(string); got != "model_cooldown" {
		t.Fatalf("code = %q, want model_cooldown", got)
	}
	if got, _ := rawErr["provider"].(string); got != "claude" {
		t.Fatalf("provider = %q, want claude", got)
	}
	if got, _ := rawErr["model"].(string); got != "claude-sonnet-5-5" {
		t.Fatalf("model = %q, want claude-sonnet-5-5", got)
	}
	if got, _ := rawErr["reset_time"].(string); got != "40m0s" {
		t.Fatalf("reset_time = %q, want 40m0s", got)
	}

	// Unusable accounts are ordered by FreeAt.
	ranked := f.selector.Rank("claude", "claude-sonnet-5-5", auths)
	if ranked[0].Auth.ID != "claude-a" || ranked[1].Auth.ID != "claude-b" {
		t.Fatalf("Rank() unusable order = %q,%q, want claude-a,claude-b", ranked[0].Auth.ID, ranked[1].Auth.ID)
	}
}

func TestPick_AllLimitedUsesLabel(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	f := newResetAwareFixture(t, now)
	f.put("codex-1", "codex", QuotaWindow{Key: "primary_window", Label: "5-hour", Utilization: 100, ResetsAt: now.Add(90 * time.Minute), Routing: true})
	labelled := &Auth{ID: "codex-1", Provider: "codex", Label: "work"}

	_, err := f.selector.Pick(context.Background(), "codex", "gpt-5.5", cliproxyexecutor.Options{}, []*Auth{labelled})
	if err == nil {
		t.Fatal("Pick() error = nil, want all-limited error")
	}
	message, _ := cooldownMessage(t, err)
	want := "All Codex accounts are limited. Earliest frees at 4:30 PM (work, 5-hour limit)."
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}

func TestAllLimitedMessage_TomorrowIncludesWeekday(t *testing.T) {
	// Tuesday 2026-10-20 15:00 local; the window frees Wednesday morning.
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	pinCooldownNow(t, now)
	freeAt := time.Date(2026, 10, 21, 9, 0, 0, 0, time.Local)

	err := NewAllLimitedError("claude", "claude-sonnet-5-5", freeAt, "claude-a", "7-day")
	var mce *modelCooldownError
	if !errors.As(err, &mce) {
		t.Fatalf("NewAllLimitedError() = %T, want *modelCooldownError", err)
	}
	message, rawErr := cooldownMessage(t, err)
	want := "All Claude accounts are limited. Earliest frees at Wed 9:00 AM (claude-a, 7-day limit)."
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
	if got, _ := rawErr["reset_seconds"].(float64); got != 18*3600 {
		t.Fatalf("reset_seconds = %v, want %d", got, 18*3600)
	}
}

func TestModelCooldownError_UpstreamPathFriendlyForClaude(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	pinCooldownNow(t, now)

	message, rawErr := cooldownMessage(t, NewModelCooldownError("m", "claude", 10*time.Minute))
	if want := "All Claude accounts are limited. Earliest frees at 3:10 PM."; message != want {
		t.Fatalf("claude message = %q, want %q", message, want)
	}
	if got, _ := rawErr["reset_seconds"].(float64); got != 600 {
		t.Fatalf("reset_seconds = %v, want 600", got)
	}

	message, _ = cooldownMessage(t, NewModelCooldownError("m", "codex", 10*time.Minute))
	if want := "All Codex accounts are limited. Earliest frees at 3:10 PM."; message != want {
		t.Fatalf("codex message = %q, want %q", message, want)
	}

	message, _ = cooldownMessage(t, NewModelCooldownError("m", "gemini", 10*time.Minute))
	if want := "All credentials for model m are cooling down via provider gemini"; message != want {
		t.Fatalf("gemini message = %q, want %q", message, want)
	}
}

// cooldownAuth builds an account blocked by a real 429: unavailable with a
// quota cooldown that frees at next. This is the state upstream availability
// filtering rejects before the reset-aware ranker runs.
func cooldownAuth(id, label string, next time.Time) *Auth {
	return &Auth{
		ID:             id,
		Provider:       "claude",
		Label:          label,
		Unavailable:    true,
		NextRetryAfter: next,
		Quota:          QuotaState{Exceeded: true, NextRecoverAt: next},
	}
}

func TestGetAvailableAuths_AllCooldownNamesEarliestAccount(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	const idA, idB = "cooldown-test-claude-a", "cooldown-test-claude-b"
	// claude-b is listed first and frees later: the earliest one must win.
	auths := []*Auth{
		cooldownAuth(idB, "claude-b", now.Add(3*time.Hour)),
		cooldownAuth(idA, "claude-a", now.Add(40*time.Minute)),
	}

	// A snapshot showing a full 5-hour window resetting at about the retry time names the window.
	DefaultQuotaSnapshots().Put(QuotaSnapshot{
		AuthID:   idA,
		Provider: "claude",
		Windows: []QuotaWindow{
			fiveHourWindow(100, now.Add(41*time.Minute)),
			sevenDayWindow(60, now.Add(24*time.Hour)),
		},
		FetchedAt: now,
	})
	t.Cleanup(func() { DefaultQuotaSnapshots().Delete(idA) })

	_, err := getAvailableAuths(auths, "claude", "claude-sonnet-5-5", now)
	if err == nil {
		t.Fatal("getAvailableAuths() error = nil, want all-limited error")
	}
	var mce *modelCooldownError
	if !errors.As(err, &mce) {
		t.Fatalf("error = %T (%v), want *modelCooldownError", err, err)
	}
	message, rawErr := cooldownMessage(t, err)
	want := "All Claude accounts are limited. Earliest frees at 3:40 PM (claude-a, 5-hour limit)."
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
	// The JSON fields of the upstream error are unchanged.
	if got, _ := rawErr["reset_seconds"].(float64); got != 2400 {
		t.Fatalf("reset_seconds = %v, want 2400", got)
	}
	if got, _ := rawErr["reset_time"].(string); got != "40m0s" {
		t.Fatalf("reset_time = %q, want 40m0s", got)
	}
	if got, _ := rawErr["code"].(string); got != "model_cooldown" {
		t.Fatalf("code = %q, want model_cooldown", got)
	}
	if got, _ := rawErr["provider"].(string); got != "claude" {
		t.Fatalf("provider = %q, want claude", got)
	}
	if got, _ := rawErr["model"].(string); got != "claude-sonnet-5-5" {
		t.Fatalf("model = %q, want claude-sonnet-5-5", got)
	}
	if got := mce.Headers().Get("Retry-After"); got != "2400" {
		t.Fatalf("Retry-After = %q, want 2400", got)
	}

	// Without a matching snapshot window the limit is reported as a cooldown.
	DefaultQuotaSnapshots().Delete(idA)
	_, err = getAvailableAuths(auths, "claude", "claude-sonnet-5-5", now)
	message, _ = cooldownMessage(t, err)
	want = "All Claude accounts are limited. Earliest frees at 3:40 PM (claude-a, cooldown limit)."
	if message != want {
		t.Fatalf("message without snapshot = %q, want %q", message, want)
	}

	// A full window resetting at a different time does not explain this cooldown.
	DefaultQuotaSnapshots().Put(QuotaSnapshot{
		AuthID:    idA,
		Provider:  "claude",
		Windows:   []QuotaWindow{sevenDayWindow(100, now.Add(24*time.Hour))},
		FetchedAt: now,
	})
	_, err = getAvailableAuths(auths, "claude", "claude-sonnet-5-5", now)
	message, _ = cooldownMessage(t, err)
	if message != want {
		t.Fatalf("message with unrelated window = %q, want %q", message, want)
	}

	// Without a label the ID names the account.
	unlabeled := []*Auth{cooldownAuth(idB, "", now.Add(3*time.Hour)), cooldownAuth(idA, "", now.Add(40*time.Minute))}
	_, err = getAvailableAuths(unlabeled, "claude", "claude-sonnet-5-5", now)
	message, _ = cooldownMessage(t, err)
	if want := "All Claude accounts are limited. Earliest frees at 3:40 PM (" + idA + ", cooldown limit)."; message != want {
		t.Fatalf("message without label = %q, want %q", message, want)
	}
}

func TestManagerAvailability_AllCooldownNamesEarliestAccount(t *testing.T) {
	now := time.Date(2026, 10, 20, 15, 0, 0, 0, time.Local)
	const idA, idB = "manager-cooldown-test-claude-a", "manager-cooldown-test-claude-b"
	auths := []*Auth{
		cooldownAuth(idB, "claude-b", now.Add(3*time.Hour)),
		cooldownAuth(idA, "claude-a", now.Add(40*time.Minute)),
	}
	DefaultQuotaSnapshots().Put(QuotaSnapshot{
		AuthID:    idA,
		Provider:  "claude",
		Windows:   []QuotaWindow{fiveHourWindow(100, now.Add(40*time.Minute))},
		FetchedAt: now,
	})
	t.Cleanup(func() { DefaultQuotaSnapshots().Delete(idA) })

	selector := NewSessionAffinitySelector(NewResetAwareSelector(ResetAwareConfig{Now: func() time.Time { return now }}))
	t.Cleanup(selector.Stop)
	manager := NewManager(nil, selector, nil)

	// This is the pass that runs before the reset-aware selector on the live
	// request path (pickNextLegacy -> availableAuthsForSelector).
	_, err := manager.availableAuthsForRouteModelAcrossPriorities(auths, "claude", "claude-sonnet-5-5", now)
	if err == nil {
		t.Fatal("availableAuthsForRouteModelAcrossPriorities() error = nil, want all-limited error")
	}
	message, rawErr := cooldownMessage(t, err)
	want := "All Claude accounts are limited. Earliest frees at 3:40 PM (claude-a, 5-hour limit)."
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
	if got, _ := rawErr["reset_seconds"].(float64); got != 2400 {
		t.Fatalf("reset_seconds = %v, want 2400", got)
	}
}
