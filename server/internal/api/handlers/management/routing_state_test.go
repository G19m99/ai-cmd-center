package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/quotapoll"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type routingStateWindowJSON struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Utilization float64 `json:"utilization"`
	ResetsAt    string  `json:"resets_at"`
	Weekly      bool    `json:"weekly"`
	ModelScope  string  `json:"model_scope"`
	Routing     bool    `json:"routing"`
}

type routingStateAccountJSON struct {
	AuthIndex      string  `json:"auth_index"`
	Provider       string  `json:"provider"`
	Label          string  `json:"label"`
	Status         string  `json:"status"`
	Urgent         bool    `json:"urgent"`
	Rank           int     `json:"rank"`
	Sessions       int     `json:"sessions"`
	LimitedBy      string  `json:"limited_by"`
	FreeAt         string  `json:"free_at"`
	WeeklyResetsAt string  `json:"weekly_resets_at"`
	Headroom       float64 `json:"headroom"`
	Quota          struct {
		FetchedAt string                   `json:"fetched_at"`
		Stale     bool                     `json:"stale"`
		LastError string                   `json:"last_error"`
		Windows   []routingStateWindowJSON `json:"windows"`
		Bonus     struct {
			Known     bool     `json:"known"`
			Available int      `json:"available"`
			Clears    []string `json:"clears"`
			ExpiresAt string   `json:"expires_at"`
		} `json:"bonus"`
	} `json:"quota"`
}

type routingStateJSON struct {
	Strategy    string `json:"strategy"`
	GeneratedAt string `json:"generated_at"`
	Providers   map[string]struct {
		UrgentAuthIndex string `json:"urgent_auth_index"`
		EarliestFreeAt  string `json:"earliest_free_at"`
	} `json:"providers"`
	Accounts []routingStateAccountJSON `json:"accounts"`
}

// routingStateFixture registers auths on a manager whose selector is the
// reset-aware strategy (affinity wrapping ResetAwareSelector), and writes
// snapshots into the process-wide store the handler reads.
type routingStateFixture struct {
	manager  *coreauth.Manager
	affinity *coreauth.SessionAffinitySelector
	handler  *Handler
}

func newRoutingStateFixture(t *testing.T, resetAware bool) *routingStateFixture {
	t.Helper()
	t.Setenv("MANAGEMENT_PASSWORD", "")
	manager := coreauth.NewManager(nil, nil, nil)
	f := &routingStateFixture{manager: manager}
	if resetAware {
		f.affinity = coreauth.NewSessionAffinitySelector(coreauth.NewResetAwareSelector(coreauth.ResetAwareConfig{}))
		manager.SetSelector(f.affinity)
	}
	f.handler = NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	return f
}

func (f *routingStateFixture) register(t *testing.T, auth *coreauth.Auth) string {
	t.Helper()
	index := auth.EnsureIndex()
	if _, err := f.manager.Register(context.Background(), auth); err != nil {
		t.Fatalf("register %s: %v", auth.ID, err)
	}
	t.Cleanup(func() { coreauth.DefaultQuotaSnapshots().Delete(auth.ID) })
	return index
}

func (f *routingStateFixture) bind(t *testing.T, sessionID string, auths ...*coreauth.Auth) {
	t.Helper()
	opts := cliproxyexecutor.Options{Metadata: map[string]any{cliproxyexecutor.DerivedSessionIDMetadataKey: sessionID}}
	if _, err := f.affinity.Pick(context.Background(), "claude", "claude-sonnet-4-5", opts, auths); err != nil {
		t.Fatalf("bind %s: %v", sessionID, err)
	}
}

func putRoutingSnapshot(authID, provider string, windows ...coreauth.QuotaWindow) {
	coreauth.DefaultQuotaSnapshots().Put(coreauth.QuotaSnapshot{
		AuthID:    authID,
		Provider:  provider,
		Windows:   windows,
		Bonus:     coreauth.BonusResets{Known: true, Available: 1, Clears: []string{"seven_day"}},
		FetchedAt: time.Now(),
	})
}

func weeklyWindow(utilization float64, resetsAt time.Time) coreauth.QuotaWindow {
	return coreauth.QuotaWindow{Key: "seven_day", Label: "7-day", Utilization: utilization, ResetsAt: resetsAt, Weekly: true, Routing: true}
}

func fiveHourWindow(utilization float64, resetsAt time.Time) coreauth.QuotaWindow {
	return coreauth.QuotaWindow{Key: "five_hour", Label: "5-hour", Utilization: utilization, ResetsAt: resetsAt, Routing: true}
}

func getRoutingState(t *testing.T, h *Handler) (routingStateJSON, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/routing-state", nil)
	h.GetRoutingState(ctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET routing-state = %d, body %s", rec.Code, rec.Body.String())
	}
	var out routingStateJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body.String())
	}
	return out, rec.Body.String()
}

func accountByIndex(t *testing.T, state routingStateJSON, index string) routingStateAccountJSON {
	t.Helper()
	for _, account := range state.Accounts {
		if account.AuthIndex == index {
			return account
		}
	}
	t.Fatalf("account %q missing from %+v", index, state.Accounts)
	return routingStateAccountJSON{}
}

func TestRoutingState_ReportsRankStatusSessions(t *testing.T) {
	f := newRoutingStateFixture(t, true)
	now := time.Now()
	a := &coreauth.Auth{ID: "rs-rank-a", FileName: "rs-rank-a.json", Provider: "claude", Label: "alice@example.com", Status: coreauth.StatusActive}
	b := &coreauth.Auth{ID: "rs-rank-b", FileName: "rs-rank-b.json", Provider: "claude", Label: "bob@example.com", Status: coreauth.StatusActive}
	aIndex := f.register(t, a)
	bIndex := f.register(t, b)
	// A resets in 12h (urgent), B in 5 days.
	putRoutingSnapshot(a.ID, "claude", fiveHourWindow(28, now.Add(3*time.Hour)), weeklyWindow(62, now.Add(12*time.Hour)))
	putRoutingSnapshot(b.ID, "claude", weeklyWindow(10, now.Add(5*24*time.Hour)))

	// Three sessions bind to A (rank 1); one is forced onto B.
	f.bind(t, "rs-s1", a, b)
	f.bind(t, "rs-s2", a, b)
	f.bind(t, "rs-s3", a, b)
	f.bind(t, "rs-s4", b)

	state, body := getRoutingState(t, f.handler)
	t.Logf("sample routing-state response: %s", body)

	if state.Strategy != "reset-aware" {
		t.Fatalf("strategy = %q, want reset-aware", state.Strategy)
	}
	if _, err := time.Parse(time.RFC3339, state.GeneratedAt); err != nil {
		t.Fatalf("generated_at %q not RFC3339: %v", state.GeneratedAt, err)
	}
	gotA := accountByIndex(t, state, aIndex)
	gotB := accountByIndex(t, state, bIndex)
	if gotA.Status != "draining" || gotA.Rank != 1 || gotA.Sessions != 3 || !gotA.Urgent {
		t.Fatalf("A = status %q rank %d sessions %d urgent %v, want draining 1 3 true", gotA.Status, gotA.Rank, gotA.Sessions, gotA.Urgent)
	}
	if gotB.Status != "standby" || gotB.Rank != 2 || gotB.Sessions != 1 || gotB.Urgent {
		t.Fatalf("B = status %q rank %d sessions %d urgent %v, want standby 2 1 false", gotB.Status, gotB.Rank, gotB.Sessions, gotB.Urgent)
	}
	if gotA.Provider != "claude" || gotA.Label != "alice@example.com" {
		t.Fatalf("A provider/label = %q/%q", gotA.Provider, gotA.Label)
	}
	if gotA.Headroom != 38 {
		t.Fatalf("A headroom = %v, want 38", gotA.Headroom)
	}
	if wantReset := now.Add(12 * time.Hour).UTC().Format(time.RFC3339); gotA.WeeklyResetsAt != wantReset {
		t.Fatalf("A weekly_resets_at = %q, want %q", gotA.WeeklyResetsAt, wantReset)
	}
	if gotA.Quota.Stale || gotA.Quota.FetchedAt == "" || len(gotA.Quota.Windows) != 2 {
		t.Fatalf("A quota = %+v", gotA.Quota)
	}
	if w := gotA.Quota.Windows[0]; w.Key != "five_hour" || w.Label != "5-hour" || w.Utilization != 28 || w.Weekly || !w.Routing || w.ResetsAt == "" {
		t.Fatalf("A first window = %+v", w)
	}
	if bonus := gotA.Quota.Bonus; !bonus.Known || bonus.Available != 1 || len(bonus.Clears) != 1 || bonus.Clears[0] != "seven_day" || bonus.ExpiresAt != "" {
		t.Fatalf("A bonus = %+v", bonus)
	}
	claude, ok := state.Providers["claude"]
	if !ok {
		t.Fatalf("providers missing claude: %+v", state.Providers)
	}
	if claude.UrgentAuthIndex != aIndex || claude.EarliestFreeAt != "" {
		t.Fatalf("claude provider = %+v, want urgent %q and no free time", claude, aIndex)
	}
	if _, ok := state.Providers["codex"]; !ok {
		t.Fatalf("providers missing codex: %+v", state.Providers)
	}
	if state.Accounts[0].AuthIndex != aIndex {
		t.Fatalf("accounts not ordered by rank: first = %q", state.Accounts[0].AuthIndex)
	}
}

func TestRoutingState_LimitedAndUnknown(t *testing.T) {
	f := newRoutingStateFixture(t, true)
	now := time.Now()
	good := &coreauth.Auth{ID: "rs-lu-good", FileName: "rs-lu-good.json", Provider: "codex", Status: coreauth.StatusActive}
	capped := &coreauth.Auth{ID: "rs-lu-capped", FileName: "rs-lu-capped.json", Provider: "codex", Status: coreauth.StatusActive}
	fresh := &coreauth.Auth{ID: "rs-lu-fresh", FileName: "rs-lu-fresh.json", Provider: "codex", Status: coreauth.StatusActive}
	cooled := &coreauth.Auth{
		ID: "rs-lu-cooled", FileName: "rs-lu-cooled.json", Provider: "codex", Status: coreauth.StatusError,
		Unavailable: true, NextRetryAfter: now.Add(40 * time.Minute),
		Quota: coreauth.QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: now.Add(40 * time.Minute)},
	}
	goodIndex := f.register(t, good)
	cappedIndex := f.register(t, capped)
	freshIndex := f.register(t, fresh)
	cooledIndex := f.register(t, cooled)

	putRoutingSnapshot(good.ID, "codex", weeklyWindow(50, now.Add(3*24*time.Hour)))
	cappedFree := now.Add(2 * time.Hour)
	putRoutingSnapshot(capped.ID, "codex", fiveHourWindow(100, cappedFree), weeklyWindow(40, now.Add(2*24*time.Hour)))
	// fresh has no snapshot at all.
	// cooled has a healthy snapshot (earliest weekly reset) but an upstream cooldown.
	putRoutingSnapshot(cooled.ID, "codex", weeklyWindow(5, now.Add(24*time.Hour+time.Hour)))

	state, _ := getRoutingState(t, f.handler)

	gotGood := accountByIndex(t, state, goodIndex)
	if gotGood.Status != "draining" || gotGood.Rank != 1 {
		t.Fatalf("good = %q rank %d, want draining 1", gotGood.Status, gotGood.Rank)
	}
	gotFresh := accountByIndex(t, state, freshIndex)
	if gotFresh.Status != "unknown" || gotFresh.Rank != 2 || !gotFresh.Quota.Stale || gotFresh.Quota.FetchedAt != "" {
		t.Fatalf("fresh = %q rank %d quota %+v, want unknown 2 stale", gotFresh.Status, gotFresh.Rank, gotFresh.Quota)
	}
	gotCapped := accountByIndex(t, state, cappedIndex)
	if gotCapped.Status != "limited" || gotCapped.Rank != 0 || gotCapped.LimitedBy != "5-hour" || gotCapped.FreeAt != cappedFree.UTC().Format(time.RFC3339) {
		t.Fatalf("capped = %+v, want limited by 5-hour until %s", gotCapped, cappedFree.UTC().Format(time.RFC3339))
	}
	gotCooled := accountByIndex(t, state, cooledIndex)
	if gotCooled.Status != "limited" || gotCooled.Rank != 0 || gotCooled.FreeAt == "" || gotCooled.LimitedBy == "" {
		t.Fatalf("cooled = %+v, want limited with a free time", gotCooled)
	}
	codex := state.Providers["codex"]
	if codex.EarliestFreeAt != gotCooled.FreeAt {
		t.Fatalf("codex earliest_free_at = %q, want %q", codex.EarliestFreeAt, gotCooled.FreeAt)
	}
	if codex.UrgentAuthIndex != "" {
		t.Fatalf("codex urgent = %q, want none", codex.UrgentAuthIndex)
	}
}

func TestRoutingState_NonResetAwareUsesAvailabilityOnly(t *testing.T) {
	f := newRoutingStateFixture(t, false)
	now := time.Now()
	ok := &coreauth.Auth{ID: "rs-rr-ok", FileName: "rs-rr-ok.json", Provider: "claude", Status: coreauth.StatusActive}
	off := &coreauth.Auth{ID: "rs-rr-off", FileName: "rs-rr-off.json", Provider: "claude", Status: coreauth.StatusDisabled, Disabled: true}
	okIndex := f.register(t, ok)
	offIndex := f.register(t, off)
	// A capped snapshot is ignored when the strategy is not reset-aware.
	putRoutingSnapshot(ok.ID, "claude", fiveHourWindow(100, now.Add(time.Hour)))

	state, _ := getRoutingState(t, f.handler)
	if state.Strategy == "reset-aware" || state.Strategy == "" {
		t.Fatalf("strategy = %q", state.Strategy)
	}
	gotOK := accountByIndex(t, state, okIndex)
	if gotOK.Rank != 0 || gotOK.Status != "standby" || len(gotOK.Quota.Windows) != 1 {
		t.Fatalf("ok = %+v, want rank 0 standby with quota", gotOK)
	}
	gotOff := accountByIndex(t, state, offIndex)
	if gotOff.Rank != 0 || gotOff.Status != "limited" || gotOff.LimitedBy != "disabled" {
		t.Fatalf("off = %+v, want rank 0 limited by disabled", gotOff)
	}
}

func TestRoutingState_RefreshWithoutPoller409(t *testing.T) {
	quotapoll.SetDefault(nil)
	f := newRoutingStateFixture(t, true)
	for _, body := range []string{`{}`, `{"auth_index":"whatever"}`, ``} {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodPost, "/v0/management/routing-state/refresh", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		ctx.Request = req
		f.handler.RefreshRoutingState(ctx)
		if rec.Code != http.StatusConflict {
			t.Fatalf("body %q: status = %d, want 409 (%s)", body, rec.Code, rec.Body.String())
		}
		var payload map[string]string
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if payload["error"] != "quota poller not running (routing.strategy is not reset-aware)" {
			t.Fatalf("error = %q", payload["error"])
		}
	}
}

func TestRoutingState_RefreshWithPoller(t *testing.T) {
	var hits atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		resets := time.Now().Add(30 * time.Hour).UTC().Format(time.RFC3339)
		_, _ = w.Write([]byte(`{"five_hour":{"utilization":12,"resets_at":"` + resets + `"},"seven_day":{"utilization":44,"resets_at":"` + resets + `"}}`))
	}))
	t.Cleanup(upstream.Close)

	f := newRoutingStateFixture(t, true)
	one := &coreauth.Auth{ID: "rs-refresh-1", FileName: "rs-refresh-1.json", Provider: "claude", Status: coreauth.StatusActive, Metadata: map[string]any{"access_token": "tok-1"}}
	two := &coreauth.Auth{ID: "rs-refresh-2", FileName: "rs-refresh-2.json", Provider: "claude", Status: coreauth.StatusActive, Metadata: map[string]any{"access_token": "tok-2"}}
	oneIndex := f.register(t, one)
	f.register(t, two)

	quotapoll.SetDefault(quotapoll.New(quotapoll.Config{
		Manager:   f.manager,
		Store:     coreauth.DefaultQuotaSnapshots(),
		Endpoints: quotapoll.Endpoints{ClaudeUsage: upstream.URL + "/api/oauth/usage"},
	}))
	t.Cleanup(func() { quotapoll.SetDefault(nil) })

	post := func(body string) (int, routingStateJSON) {
		rec := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(rec)
		req := httptest.NewRequest(http.MethodPost, "/v0/management/routing-state/refresh", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		ctx.Request = req
		f.handler.RefreshRoutingState(ctx)
		var out routingStateJSON
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
		}
		return rec.Code, out
	}

	code, state := post(`{"auth_index":"` + oneIndex + `"}`)
	if code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("single refresh = %d with %d upstream hits, want 200 and 1", code, hits.Load())
	}
	if got := accountByIndex(t, state, oneIndex); got.Quota.Stale || len(got.Quota.Windows) != 2 {
		t.Fatalf("refreshed account quota = %+v", got.Quota)
	}

	if code, _ = post(`{"auth_index":"no-such-index"}`); code != http.StatusNotFound {
		t.Fatalf("unknown auth_index = %d, want 404", code)
	}

	code, state = post(``)
	if code != http.StatusOK || hits.Load() != 3 {
		t.Fatalf("refresh all = %d with %d upstream hits, want 200 and 3", code, hits.Load())
	}
	for _, account := range state.Accounts {
		if account.Quota.Stale {
			t.Fatalf("account %s still stale after refresh all", account.AuthIndex)
		}
	}
}
