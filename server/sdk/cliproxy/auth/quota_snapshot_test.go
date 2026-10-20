package auth

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func readQuotaFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "quota", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

func pinQuotaNow(t *testing.T, now time.Time) {
	t.Helper()
	previous := quotaNow
	quotaNow = func() time.Time { return now }
	t.Cleanup(func() { quotaNow = previous })
}

func findQuotaWindow(t *testing.T, windows []QuotaWindow, key string) QuotaWindow {
	t.Helper()
	for _, window := range windows {
		if window.Key == key {
			return window
		}
	}
	t.Fatalf("window %q not found in %+v", key, windows)
	return QuotaWindow{}
}

func hasQuotaWindow(windows []QuotaWindow, key string) bool {
	for _, window := range windows {
		if window.Key == key {
			return true
		}
	}
	return false
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

func TestParseClaudeUsage_MapsWindows(t *testing.T) {
	windows, err := ParseClaudeUsage(readQuotaFixture(t, "claude_usage.json"))
	if err != nil {
		t.Fatalf("ParseClaudeUsage: %v", err)
	}
	if len(windows) != 5 {
		t.Fatalf("expected 5 windows, got %d: %+v", len(windows), windows)
	}

	fiveHour := findQuotaWindow(t, windows, "five_hour")
	if fiveHour.Label != "5-hour" || fiveHour.Weekly || !fiveHour.Routing || fiveHour.ModelScope != "" {
		t.Fatalf("unexpected five_hour window: %+v", fiveHour)
	}
	if fiveHour.Utilization != 42.5 {
		t.Fatalf("five_hour utilization = %v, want 42.5", fiveHour.Utilization)
	}
	if !fiveHour.ResetsAt.Equal(mustTime(t, "2026-10-09T18:00:00Z")) {
		t.Fatalf("five_hour resets_at = %v", fiveHour.ResetsAt)
	}

	sevenDay := findQuotaWindow(t, windows, "seven_day")
	if sevenDay.Label != "7-day" || !sevenDay.Weekly || !sevenDay.Routing || sevenDay.ModelScope != "" {
		t.Fatalf("unexpected seven_day window: %+v", sevenDay)
	}
	if sevenDay.Utilization != 16 || !sevenDay.ResetsAt.Equal(mustTime(t, "2026-10-15T09:00:00Z")) {
		t.Fatalf("unexpected seven_day values: %+v", sevenDay)
	}

	opus := findQuotaWindow(t, windows, "seven_day_opus")
	if opus.Label != "7-day Opus" || !opus.Weekly || !opus.Routing || opus.ModelScope != "opus" {
		t.Fatalf("unexpected seven_day_opus window: %+v", opus)
	}
	if opus.Utilization != 55 || !opus.ResetsAt.Equal(mustTime(t, "2026-10-15T09:00:00.123456789Z")) {
		t.Fatalf("unexpected seven_day_opus values: %+v", opus)
	}

	fable := findQuotaWindow(t, windows, "iguana_necktie")
	if fable.Label != "7-day Fable" || !fable.Weekly || !fable.Routing || fable.ModelScope != "fable" {
		t.Fatalf("unexpected iguana_necktie window: %+v", fable)
	}
	if fable.Utilization != 12 {
		t.Fatalf("iguana_necktie utilization = %v, want 12", fable.Utilization)
	}

	oauthApps := findQuotaWindow(t, windows, "seven_day_oauth_apps")
	if oauthApps.Routing || !oauthApps.Weekly || oauthApps.Utilization != 3 {
		t.Fatalf("unexpected seven_day_oauth_apps window: %+v", oauthApps)
	}

	for _, absent := range []string{"seven_day_sonnet", "seven_day_cowork", "extra_usage", "additional_credit_balance", "unrelated"} {
		if hasQuotaWindow(windows, absent) {
			t.Fatalf("window %q should be omitted", absent)
		}
	}
}

func TestParseClaudeUsage_FableFromLimitsAndCreditPool(t *testing.T) {
	// A weekly_scoped Fable limit replaces the iguana_necktie key.
	body := []byte(`{
		"five_hour": {"utilization": 1, "resets_at": "2026-10-20T15:00:00Z"},
		"iguana_necktie": {"utilization": 77, "resets_at": "2026-10-25T00:00:00Z"},
		"limits": [
			{"kind": "weekly_scoped", "percent": 33, "resets_at": "2026-10-26T00:00:00Z", "is_active": true,
			 "scope": {"model": {"id": "fable-5", "display_name": "Fable 5"}}}
		]
	}`)
	windows, err := ParseClaudeUsage(body)
	if err != nil {
		t.Fatalf("ParseClaudeUsage: %v", err)
	}
	fable := findQuotaWindow(t, windows, "iguana_necktie")
	if fable.Utilization != 33 || fable.ModelScope != "fable" || !fable.Weekly {
		t.Fatalf("expected Fable window from limits, got %+v", fable)
	}
	if !fable.ResetsAt.Equal(mustTime(t, "2026-10-26T00:00:00Z")) {
		t.Fatalf("fable resets_at = %v", fable.ResetsAt)
	}

	// A dollar-denominated iguana_necktie is a credit pool, not a model window.
	body = []byte(`{
		"five_hour": {"utilization": 1, "resets_at": "2026-10-20T15:00:00Z"},
		"iguana_necktie": {"limit_dollars": 100, "used_dollars": 5, "remaining_dollars": 95, "utilization": 5, "resets_at": "2026-11-01T00:00:00Z"}
	}`)
	windows, err = ParseClaudeUsage(body)
	if err != nil {
		t.Fatalf("ParseClaudeUsage: %v", err)
	}
	if hasQuotaWindow(windows, "iguana_necktie") {
		t.Fatalf("credit pool should not produce a window: %+v", windows)
	}
}

func TestParseClaudeUsage_RejectsBadPayload(t *testing.T) {
	if _, err := ParseClaudeUsage([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if _, err := ParseClaudeUsage([]byte(`{"extra_usage": {"utilization": 1}}`)); err == nil {
		t.Fatal("expected error when no quota windows are present")
	}
}

func TestParseClaudeBonus_CountsUsableGrants(t *testing.T) {
	pinQuotaNow(t, mustTime(t, "2026-10-20T12:00:00Z"))

	bonus := ParseClaudeBonus(readQuotaFixture(t, "claude_usage_cedar.json"))
	if !bonus.Known {
		t.Fatalf("expected Known, got %+v", bonus)
	}
	if bonus.Error != "" {
		t.Fatalf("unexpected error %q", bonus.Error)
	}
	if bonus.Available != 1 {
		t.Fatalf("Available = %d, want 1 (paused and spent grants excluded)", bonus.Available)
	}
	if len(bonus.Clears) != 1 || bonus.Clears[0] != "seven_day" {
		t.Fatalf("Clears = %v, want [seven_day]", bonus.Clears)
	}
	if !bonus.ExpiresAt.Equal(mustTime(t, "2026-11-01T00:00:00Z")) {
		t.Fatalf("ExpiresAt = %v", bonus.ExpiresAt)
	}

	missing := ParseClaudeBonus(readQuotaFixture(t, "claude_usage.json"))
	if missing.Known {
		t.Fatalf("expected Known=false for a missing cedar_ember block, got %+v", missing)
	}
	if missing.Error == "" {
		t.Fatal("expected Error to describe the missing block")
	}

	malformed := ParseClaudeBonus([]byte(`{"cedar_ember": {"eligible": true, "grants": [{"id": "x", "resets_total": 1, "resets_left": 5}]}}`))
	if malformed.Known {
		t.Fatalf("expected Known=false for a malformed grant, got %+v", malformed)
	}

	ineligible := ParseClaudeBonus([]byte(`{"cedar_ember": {"eligible": false, "ineligible_reason": "tier", "grants": [{"id": "g", "resets_total": 1, "resets_left": 1, "usable_now": true}]}}`))
	if !ineligible.Known || ineligible.Available != 0 {
		t.Fatalf("ineligible block should be Known with 0 available, got %+v", ineligible)
	}
}

func TestParseCodexUsage_PrimarySecondaryAndAdditional(t *testing.T) {
	now := mustTime(t, "2026-10-20T12:00:00Z")
	pinQuotaNow(t, now)

	windows, err := ParseCodexUsage(readQuotaFixture(t, "codex_usage.json"))
	if err != nil {
		t.Fatalf("ParseCodexUsage: %v", err)
	}

	primary := findQuotaWindow(t, windows, "primary_window")
	if primary.Label != "5-hour" || primary.Weekly || !primary.Routing || primary.ModelScope != "" {
		t.Fatalf("unexpected primary window: %+v", primary)
	}
	if primary.Utilization != 37 {
		t.Fatalf("primary utilization = %v, want 37", primary.Utilization)
	}
	if want := now.Add(7200 * time.Second); !primary.ResetsAt.Equal(want) {
		t.Fatalf("primary resets_at = %v, want %v (quotaNow + reset_after_seconds)", primary.ResetsAt, want)
	}

	secondary := findQuotaWindow(t, windows, "secondary_window")
	if secondary.Label != "Weekly" || !secondary.Weekly || !secondary.Routing || secondary.ModelScope != "" {
		t.Fatalf("unexpected secondary window: %+v", secondary)
	}
	if secondary.Utilization != 61 || !secondary.ResetsAt.Equal(time.Unix(1785902974, 0)) {
		t.Fatalf("unexpected secondary values: %+v", secondary)
	}

	var additional []QuotaWindow
	for _, window := range windows {
		if window.ModelScope != "" {
			additional = append(additional, window)
		}
	}
	if len(additional) != 1 {
		t.Fatalf("expected one additional window, got %+v", additional)
	}
	if additional[0].ModelScope != "gpt-5.3-codex-spark" || !additional[0].Weekly || !additional[0].Routing {
		t.Fatalf("unexpected additional window: %+v", additional[0])
	}
	if additional[0].Utilization != 0 || !additional[0].ResetsAt.Equal(time.Unix(1785903197, 0)) {
		t.Fatalf("unexpected additional values: %+v", additional[0])
	}

	var review []QuotaWindow
	for _, window := range windows {
		if !window.Routing {
			review = append(review, window)
		}
	}
	if len(review) != 1 || review[0].Utilization != 5 || review[0].Weekly {
		t.Fatalf("expected one non-routing code review window, got %+v", review)
	}
	if len(windows) != 4 {
		t.Fatalf("expected 4 windows, got %d: %+v", len(windows), windows)
	}
}

func TestParseCodexUsage_LimitReachedWithoutPercent(t *testing.T) {
	pinQuotaNow(t, mustTime(t, "2026-10-20T12:00:00Z"))
	body := []byte(`{"rate_limit": {"allowed": false, "limit_reached": true,
		"primary_window": {"limit_window_seconds": 18000, "reset_after_seconds": 100},
		"secondary_window": {"used_percent": "12.5", "limit_window_seconds": 604800, "reset_at": "1785902974"}}}`)
	windows, err := ParseCodexUsage(body)
	if err != nil {
		t.Fatalf("ParseCodexUsage: %v", err)
	}
	primary := findQuotaWindow(t, windows, "primary_window")
	if primary.Utilization != 100 {
		t.Fatalf("limit reached without used_percent should read 100, got %+v", primary)
	}
	secondary := findQuotaWindow(t, windows, "secondary_window")
	if secondary.Utilization != 12.5 || !secondary.ResetsAt.Equal(time.Unix(1785902974, 0)) {
		t.Fatalf("string-encoded values should parse: %+v", secondary)
	}
	if _, err := ParseCodexUsage([]byte(`[]`)); err == nil {
		t.Fatal("expected error for non-object payload")
	}
}

func TestParseCodexResetCredits(t *testing.T) {
	bonus := ParseCodexResetCredits(readQuotaFixture(t, "codex_reset_credits.json"))
	if !bonus.Known || bonus.Error != "" {
		t.Fatalf("expected Known bonus without error, got %+v", bonus)
	}
	if bonus.Available != 2 {
		t.Fatalf("Available = %d, want applicable_available_count 2", bonus.Available)
	}
	if !bonus.ExpiresAt.Equal(mustTime(t, "2026-10-28T00:00:00Z")) {
		t.Fatalf("ExpiresAt = %v, want earliest available credit expiry", bonus.ExpiresAt)
	}

	fallback := ParseCodexResetCredits([]byte(`{"available_count": 4, "credits": []}`))
	if !fallback.Known || fallback.Available != 4 || !fallback.ExpiresAt.IsZero() {
		t.Fatalf("expected available_count fallback, got %+v", fallback)
	}

	invalid := ParseCodexResetCredits([]byte(`{"unexpected": true}`))
	if invalid.Known || invalid.Error == "" {
		t.Fatalf("expected unknown bonus for unexpected shape, got %+v", invalid)
	}
	if garbage := ParseCodexResetCredits([]byte(`nope`)); garbage.Known {
		t.Fatalf("expected unknown bonus for invalid JSON, got %+v", garbage)
	}
}

func TestSnapshotStore_FailureKeepsLastAndMarksStale(t *testing.T) {
	store := NewQuotaSnapshotStore()
	fetchedAt := mustTime(t, "2026-10-20T12:00:00Z")
	store.Put(QuotaSnapshot{
		AuthID:    "acct-1",
		Provider:  "claude",
		Windows:   []QuotaWindow{{Key: "seven_day", Label: "7-day", Utilization: 40, Weekly: true, Routing: true}},
		Bonus:     BonusResets{Known: true, Available: 1, Clears: []string{"seven_day"}},
		FetchedAt: fetchedAt,
	})

	failedAt := fetchedAt.Add(20 * time.Minute)
	store.MarkFailed("acct-1", "claude", errors.New("upstream 503"), failedAt)

	snap, ok := store.Get("acct-1")
	if !ok {
		t.Fatal("expected snapshot to still exist")
	}
	if len(snap.Windows) != 1 || snap.Windows[0].Key != "seven_day" {
		t.Fatalf("MarkFailed should keep previous windows, got %+v", snap.Windows)
	}
	if !snap.Bonus.Known || snap.Bonus.Available != 1 {
		t.Fatalf("MarkFailed should keep previous bonus, got %+v", snap.Bonus)
	}
	if !snap.FetchedAt.Equal(fetchedAt) {
		t.Fatalf("MarkFailed should keep FetchedAt, got %v", snap.FetchedAt)
	}
	if snap.LastError != "upstream 503" || !snap.LastErrorAt.Equal(failedAt) {
		t.Fatalf("unexpected error fields: %q at %v", snap.LastError, snap.LastErrorAt)
	}
	if !snap.Stale(failedAt, 15*time.Minute) {
		t.Fatal("snapshot fetched 20m ago should be stale at 15m")
	}
	if snap.Stale(fetchedAt.Add(10*time.Minute), 15*time.Minute) {
		t.Fatal("snapshot fetched 10m ago should not be stale at 15m")
	}

	store.MarkFailed("unknown", "codex", errors.New("boom"), failedAt)
	created, ok := store.Get("unknown")
	if !ok {
		t.Fatal("MarkFailed on an unknown ID should create a snapshot")
	}
	if created.Provider != "codex" || created.AuthID != "unknown" || created.LastError != "boom" {
		t.Fatalf("unexpected created snapshot: %+v", created)
	}
	if !created.FetchedAt.IsZero() || !created.Stale(failedAt, time.Hour) {
		t.Fatalf("created snapshot should have zero FetchedAt and be stale: %+v", created)
	}

	store.Delete("acct-1")
	if _, ok := store.Get("acct-1"); ok {
		t.Fatal("Delete should remove the snapshot")
	}
	if _, ok := store.Get("never"); ok {
		t.Fatal("Get on an unknown ID should report false")
	}
}

func TestSnapshotStore_GetReturnsCopy(t *testing.T) {
	store := NewQuotaSnapshotStore()
	original := QuotaSnapshot{
		AuthID:    "acct-2",
		Provider:  "codex",
		Windows:   []QuotaWindow{{Key: "primary_window", Utilization: 10}},
		Bonus:     BonusResets{Known: true, Clears: []string{"primary_window"}},
		FetchedAt: mustTime(t, "2026-10-20T12:00:00Z"),
	}
	store.Put(original)

	// Mutating the caller's slice after Put must not leak into the store.
	original.Windows[0].Utilization = 99
	original.Bonus.Clears[0] = "mutated"

	first, _ := store.Get("acct-2")
	if first.Windows[0].Utilization != 10 || first.Bonus.Clears[0] != "primary_window" {
		t.Fatalf("Put should copy inputs, got %+v", first)
	}

	first.Windows[0].Utilization = 55
	first.Windows = append(first.Windows, QuotaWindow{Key: "extra"})
	first.Bonus.Clears[0] = "changed"

	second, _ := store.Get("acct-2")
	if len(second.Windows) != 1 || second.Windows[0].Utilization != 10 {
		t.Fatalf("Get should return a copy, got %+v", second.Windows)
	}
	if second.Bonus.Clears[0] != "primary_window" {
		t.Fatalf("Get should deep copy Clears, got %+v", second.Bonus.Clears)
	}

	if DefaultQuotaSnapshots() == nil || DefaultQuotaSnapshots() != DefaultQuotaSnapshots() {
		t.Fatal("DefaultQuotaSnapshots should return a stable singleton")
	}
}
