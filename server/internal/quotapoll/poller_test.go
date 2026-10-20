package quotapoll

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

const (
	testClaudeToken = "tok-claude-123"
	testCodexToken  = "tok-codex-456"
	testAccountID   = "acct-123"
)

var testNow = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "sdk", "cliproxy", "auth", "testdata", "quota", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return raw
}

// claudeCedarFixture serves the cedar_ember fixture with the usable grant's
// expiry pushed far out so the test does not depend on the wall clock.
func claudeCedarFixture(t *testing.T) []byte {
	t.Helper()
	raw := string(readFixture(t, "claude_usage_cedar.json"))
	return []byte(strings.ReplaceAll(raw, "2026-11-01T00:00:00Z", "2099-01-01T00:00:00Z"))
}

func fakeIDToken(t *testing.T, accountID string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`))
	return header + "." + payload + ".sig"
}

func newAuth(id, provider string, metadata map[string]any) *coreauth.Auth {
	return &coreauth.Auth{ID: id, Provider: provider, Metadata: metadata}
}

func newManager(t *testing.T, auths ...*coreauth.Auth) *coreauth.Manager {
	t.Helper()
	manager := coreauth.NewManager(nil, nil, nil)
	for _, auth := range auths {
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatalf("register %s: %v", auth.ID, err)
		}
	}
	return manager
}

// testServer records every request and serves the fixtures from one host.
type testServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []*http.Request
	claude   func(w http.ResponseWriter, r *http.Request)
	codex    func(w http.ResponseWriter, r *http.Request)
	credits  func(w http.ResponseWriter, r *http.Request)
	hits     atomic.Int64
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	ts := &testServer{}
	ts.claude = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(claudeCedarFixture(t)) }
	ts.codex = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(readFixture(t, "codex_usage.json")) }
	ts.credits = func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(readFixture(t, "codex_reset_credits.json"))
	}
	ts.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ts.hits.Add(1)
		ts.mu.Lock()
		ts.requests = append(ts.requests, r.Clone(context.Background()))
		ts.mu.Unlock()
		lower := strings.ToLower(r.URL.Path)
		if strings.Contains(lower, "reset_rate_limits") || strings.Contains(lower, "consume") || strings.Contains(lower, "redeem") {
			t.Errorf("poller must never call a redeem/consume endpoint, got %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		switch r.URL.Path {
		case "/api/oauth/usage":
			ts.claude(w, r)
		case "/backend-api/wham/usage":
			ts.codex(w, r)
		case "/backend-api/wham/rate-limit-reset-credits":
			ts.credits(w, r)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (ts *testServer) endpoints() Endpoints {
	return Endpoints{
		ClaudeUsage:  ts.URL + "/api/oauth/usage",
		CodexUsage:   ts.URL + "/backend-api/wham/usage",
		CodexCredits: ts.URL + "/backend-api/wham/rate-limit-reset-credits",
	}
}

func (ts *testServer) requestsTo(path string) []*http.Request {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	var out []*http.Request
	for _, r := range ts.requests {
		if r.URL.Path == path {
			out = append(out, r)
		}
	}
	return out
}

func newTestPoller(t *testing.T, ts *testServer, manager *coreauth.Manager) (*Poller, *coreauth.QuotaSnapshotStore) {
	t.Helper()
	store := coreauth.NewQuotaSnapshotStore()
	p := New(Config{
		Manager:   manager,
		Store:     store,
		Interval:  5 * time.Minute,
		Endpoints: ts.endpoints(),
	})
	p.now = func() time.Time { return testNow }
	return p, store
}

func findWindow(t *testing.T, windows []coreauth.QuotaWindow, key string) coreauth.QuotaWindow {
	t.Helper()
	for _, w := range windows {
		if w.Key == key {
			return w
		}
	}
	t.Fatalf("window %q not found in %+v", key, windows)
	return coreauth.QuotaWindow{}
}

func TestPoller_RefreshClaudeStoresWindowsAndBonus(t *testing.T) {
	ts := newTestServer(t)
	manager := newManager(t, newAuth("claude-1", "claude", map[string]any{"access_token": testClaudeToken}))
	p, store := newTestPoller(t, ts, manager)

	if err := p.Refresh(context.Background(), "claude-1"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	reqs := ts.requestsTo("/api/oauth/usage")
	if len(reqs) != 1 {
		t.Fatalf("claude usage requests = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if got := req.URL.Query().Get("cedar_ember"); got != "1" {
		t.Errorf("cedar_ember query = %q, want 1", got)
	}
	if got := req.URL.Query().Get("skip_spend"); got != "1" {
		t.Errorf("skip_spend query = %q, want 1", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer "+testClaudeToken {
		t.Errorf("Authorization = %q", got)
	}
	if got := req.Header.Get("anthropic-beta"); got != "oauth-2025-04-20" {
		t.Errorf("anthropic-beta = %q", got)
	}
	if got := req.Header.Get("User-Agent"); got != "claude-cli/2.1.280 (external, cli)" {
		t.Errorf("User-Agent = %q", got)
	}

	snap, ok := store.Get("claude-1")
	if !ok {
		t.Fatal("snapshot not stored")
	}
	if snap.Provider != "claude" {
		t.Errorf("Provider = %q, want claude", snap.Provider)
	}
	if !snap.FetchedAt.Equal(testNow) {
		t.Errorf("FetchedAt = %v, want %v", snap.FetchedAt, testNow)
	}
	if snap.LastError != "" {
		t.Errorf("LastError = %q, want empty", snap.LastError)
	}
	if w := findWindow(t, snap.Windows, "seven_day"); w.Utilization != 90 || !w.Weekly {
		t.Errorf("seven_day = %+v", w)
	}
	findWindow(t, snap.Windows, "five_hour")
	if !snap.Bonus.Known || snap.Bonus.Available != 1 {
		t.Errorf("Bonus = %+v, want Known with Available 1", snap.Bonus)
	}
}

func TestPoller_RefreshCodexUsesAccountHeaderAndCredits(t *testing.T) {
	t.Run("id_token derives account id and credits give bonus", func(t *testing.T) {
		ts := newTestServer(t)
		manager := newManager(t, newAuth("codex-1", "codex", map[string]any{
			"access_token": testCodexToken,
			"id_token":     fakeIDToken(t, testAccountID),
		}))
		p, store := newTestPoller(t, ts, manager)

		if err := p.Refresh(context.Background(), "codex-1"); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		for _, path := range []string{"/backend-api/wham/usage", "/backend-api/wham/rate-limit-reset-credits"} {
			reqs := ts.requestsTo(path)
			if len(reqs) != 1 {
				t.Fatalf("%s requests = %d, want 1", path, len(reqs))
			}
			if got := reqs[0].Header.Get("Chatgpt-Account-Id"); got != testAccountID {
				t.Errorf("%s Chatgpt-Account-Id = %q, want %q", path, got, testAccountID)
			}
			if got := reqs[0].Header.Get("Authorization"); got != "Bearer "+testCodexToken {
				t.Errorf("%s Authorization = %q", path, got)
			}
		}
		snap, ok := store.Get("codex-1")
		if !ok {
			t.Fatal("snapshot not stored")
		}
		if w := findWindow(t, snap.Windows, "secondary_window"); w.Utilization != 61 || !w.Weekly {
			t.Errorf("secondary_window = %+v", w)
		}
		if !snap.Bonus.Known || snap.Bonus.Available != 2 {
			t.Errorf("Bonus = %+v, want Known with Available 2", snap.Bonus)
		}
	})

	t.Run("metadata chatgpt_account_id wins over id_token", func(t *testing.T) {
		ts := newTestServer(t)
		manager := newManager(t, newAuth("codex-2", "codex", map[string]any{
			"access_token":       testCodexToken,
			"chatgpt_account_id": "acct-direct",
			"id_token":           fakeIDToken(t, testAccountID),
		}))
		p, _ := newTestPoller(t, ts, manager)
		if err := p.Refresh(context.Background(), "codex-2"); err != nil {
			t.Fatalf("Refresh: %v", err)
		}
		if got := ts.requestsTo("/backend-api/wham/usage")[0].Header.Get("Chatgpt-Account-Id"); got != "acct-direct" {
			t.Errorf("Chatgpt-Account-Id = %q, want acct-direct", got)
		}
	})

	t.Run("credits 500 keeps windows with unknown bonus", func(t *testing.T) {
		ts := newTestServer(t)
		ts.credits = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
		manager := newManager(t, newAuth("codex-3", "codex", map[string]any{
			"access_token": testCodexToken,
			"id_token":     fakeIDToken(t, testAccountID),
		}))
		p, store := newTestPoller(t, ts, manager)

		if err := p.Refresh(context.Background(), "codex-3"); err != nil {
			t.Fatalf("Refresh should succeed when only credits fail, got %v", err)
		}
		snap, ok := store.Get("codex-3")
		if !ok {
			t.Fatal("snapshot not stored")
		}
		if len(snap.Windows) == 0 {
			t.Fatal("windows should be stored despite credits failure")
		}
		if snap.Bonus.Known {
			t.Errorf("Bonus.Known = true, want false")
		}
		if snap.Bonus.Error == "" {
			t.Errorf("Bonus.Error should describe the credits failure")
		}
		if snap.LastError != "" {
			t.Errorf("LastError = %q, want empty (usage succeeded)", snap.LastError)
		}
	})
}

func TestPoller_FailureMarksFailedAndBacksOff(t *testing.T) {
	ts := newTestServer(t)
	ts.claude = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
	manager := newManager(t, newAuth("claude-1", "claude", map[string]any{"access_token": testClaudeToken}))
	p, store := newTestPoller(t, ts, manager)

	previous := coreauth.QuotaSnapshot{
		AuthID:    "claude-1",
		Provider:  "claude",
		Windows:   []coreauth.QuotaWindow{{Key: "seven_day", Utilization: 10, Weekly: true, Routing: true}},
		FetchedAt: testNow.Add(-time.Hour),
	}
	store.Put(previous)

	err := p.Refresh(context.Background(), "claude-1")
	if err == nil {
		t.Fatal("Refresh should fail on 401")
	}
	var statusErr *StatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("error = %v, want StatusError 401", err)
	}

	snap, ok := store.Get("claude-1")
	if !ok {
		t.Fatal("snapshot missing")
	}
	if snap.LastError == "" || !snap.LastErrorAt.Equal(testNow) {
		t.Errorf("LastError = %q at %v, want set at %v", snap.LastError, snap.LastErrorAt, testNow)
	}
	if len(snap.Windows) != 1 || snap.Windows[0].Key != "seven_day" {
		t.Errorf("previous windows should be kept, got %+v", snap.Windows)
	}
	if !snap.FetchedAt.Equal(previous.FetchedAt) {
		t.Errorf("FetchedAt changed to %v", snap.FetchedAt)
	}

	// The first retry waits one interval, so one transient failure is retried
	// before the snapshot goes stale.
	want := testNow.Add(p.interval)
	if got := p.nextAttempt("claude-1"); !got.Equal(want) {
		t.Errorf("nextAttempt after 1 failure = %v, want %v (1x interval)", got, want)
	}

	// A sweep inside the backoff window must not hit the server again.
	before := ts.hits.Load()
	p.sweep(context.Background())
	if got := ts.hits.Load(); got != before {
		t.Errorf("sweep during backoff made %d requests, want 0", got-before)
	}

	// Second failure doubles; the cap is 30 minutes.
	p.now = func() time.Time { return want }
	_ = p.Refresh(context.Background(), "claude-1")
	if got := p.nextAttempt("claude-1"); !got.Equal(want.Add(2 * p.interval)) {
		t.Errorf("nextAttempt after 2 failures = %v, want %v (2x interval)", got, want.Add(2*p.interval))
	}
	for i := 0; i < 6; i++ {
		_ = p.Refresh(context.Background(), "claude-1")
	}
	if got := p.nextAttempt("claude-1"); !got.Equal(want.Add(maxBackoff)) {
		t.Errorf("nextAttempt should cap at %v, got %v", want.Add(maxBackoff), got)
	}

	// A success clears the backoff.
	ts.claude = func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(claudeCedarFixture(t)) }
	if err := p.Refresh(context.Background(), "claude-1"); err != nil {
		t.Fatalf("Refresh after recovery: %v", err)
	}
	if got := p.nextAttempt("claude-1"); !got.IsZero() {
		t.Errorf("nextAttempt after success = %v, want zero", got)
	}
	snap, _ = store.Get("claude-1")
	if snap.LastError != "" {
		t.Errorf("LastError = %q after success, want cleared", snap.LastError)
	}
}

func TestPoller_NetworkErrorMarksFailed(t *testing.T) {
	ts := newTestServer(t)
	manager := newManager(t, newAuth("claude-1", "claude", map[string]any{"access_token": testClaudeToken}))
	p, store := newTestPoller(t, ts, manager)
	ts.Close()

	if err := p.Refresh(context.Background(), "claude-1"); err == nil {
		t.Fatal("Refresh should fail when the server is unreachable")
	}
	snap, ok := store.Get("claude-1")
	if !ok || snap.LastError == "" {
		t.Errorf("network error should MarkFailed, got %+v (ok=%v)", snap, ok)
	}
	if got := p.nextAttempt("claude-1"); !got.Equal(testNow.Add(p.interval)) {
		t.Errorf("nextAttempt = %v, want %v (1x interval)", got, testNow.Add(p.interval))
	}
}

func TestPoller_CancelledContextIsNotAFailure(t *testing.T) {
	ts := newTestServer(t)
	started := make(chan struct{}, 1)
	ts.claude = func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done() // block until the client gives up
	}
	manager := newManager(t, newAuth("claude-1", "claude", map[string]any{"access_token": testClaudeToken}))
	p, store := newTestPoller(t, ts, manager)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- p.Refresh(ctx, "claude-1") }()
	<-started
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Refresh should return the cancellation error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Refresh did not return after cancel")
	}
	if snap, ok := store.Get("claude-1"); ok && snap.LastError != "" {
		t.Errorf("cancellation must not MarkFailed, got LastError %q", snap.LastError)
	}
	if got := p.nextAttempt("claude-1"); !got.IsZero() {
		t.Errorf("cancellation must not record backoff, got nextAttempt %v", got)
	}
}

func TestPoller_NeverCallsRedeem(t *testing.T) {
	ts := newTestServer(t)
	manager := newManager(t,
		newAuth("claude-1", "claude", map[string]any{"access_token": testClaudeToken}),
		newAuth("codex-1", "codex", map[string]any{"access_token": testCodexToken, "id_token": fakeIDToken(t, testAccountID)}),
	)
	p, _ := newTestPoller(t, ts, manager)
	p.sweep(context.Background())
	if err := p.Refresh(context.Background(), "codex-1"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	ts.mu.Lock()
	defer ts.mu.Unlock()
	for _, r := range ts.requests {
		lower := strings.ToLower(r.URL.String())
		if strings.Contains(lower, "reset_rate_limits") || strings.Contains(lower, "consume") || strings.Contains(lower, "redeem") {
			t.Errorf("forbidden request %s %s", r.Method, r.URL)
		}
		if r.Method != http.MethodGet {
			t.Errorf("poller must only read, got %s %s", r.Method, r.URL)
		}
	}
	if len(ts.requests) == 0 {
		t.Fatal("expected usage requests")
	}
}

func TestPoller_RefreshSoonDebounces(t *testing.T) {
	ts := newTestServer(t)
	manager := newManager(t, newAuth("claude-1", "claude", map[string]any{"access_token": testClaudeToken}))
	p, store := newTestPoller(t, ts, manager)

	p.RefreshSoon("claude-1")
	p.RefreshSoon("claude-1")
	p.RefreshSoon("claude-1")
	p.wait()

	if got := ts.hits.Load(); got != 1 {
		t.Fatalf("3 RefreshSoon calls within 30s made %d fetches, want 1", got)
	}
	if _, ok := store.Get("claude-1"); !ok {
		t.Fatal("RefreshSoon should store a snapshot")
	}

	// Past the debounce window a new call fetches again.
	p.now = func() time.Time { return testNow.Add(refreshSoonDebounce + time.Second) }
	p.RefreshSoon("claude-1")
	p.wait()
	if got := ts.hits.Load(); got != 2 {
		t.Fatalf("RefreshSoon after the window made %d total fetches, want 2", got)
	}

	// Unknown and unsupported auths are ignored without a fetch.
	p.RefreshSoon("missing")
	p.wait()
	if got := ts.hits.Load(); got != 2 {
		t.Fatalf("RefreshSoon(missing) made a fetch")
	}
}

func TestPoller_SkipsDisabledAndOtherProviders(t *testing.T) {
	ts := newTestServer(t)
	disabled := newAuth("claude-off", "claude", map[string]any{"access_token": testClaudeToken})
	disabled.Disabled = true
	manager := newManager(t,
		newAuth("claude-1", "claude", map[string]any{"access_token": testClaudeToken}),
		disabled,
		newAuth("gemini-1", "gemini", map[string]any{"access_token": "tok-gemini"}),
	)
	p, store := newTestPoller(t, ts, manager)
	store.Put(coreauth.QuotaSnapshot{AuthID: "ghost", Provider: "claude", FetchedAt: testNow})

	p.sweep(context.Background())

	if got := ts.hits.Load(); got != 1 {
		t.Fatalf("sweep made %d requests, want 1 (only the enabled claude auth)", got)
	}
	if _, ok := store.Get("claude-1"); !ok {
		t.Error("enabled claude auth should have a snapshot")
	}
	if _, ok := store.Get("claude-off"); ok {
		t.Error("disabled auth should not be polled")
	}
	if _, ok := store.Get("gemini-1"); ok {
		t.Error("gemini auth should not be polled")
	}
	if _, ok := store.Get("ghost"); ok {
		t.Error("snapshot for a removed auth should be deleted")
	}

	if err := p.Refresh(context.Background(), "gemini-1"); err == nil {
		t.Error("Refresh on an unsupported provider should return an error")
	}
	if err := p.Refresh(context.Background(), "claude-off"); err == nil {
		t.Error("Refresh on a disabled auth should return an error")
	}
	if err := p.Refresh(context.Background(), "ghost"); err == nil {
		t.Error("Refresh on a missing auth should return an error")
	}
	if got := ts.hits.Load(); got != 1 {
		t.Fatalf("Refresh on skipped auths made requests: total %d, want 1", got)
	}
}

func TestPoller_StartSweepsImmediatelyAndWaitsWithJitter(t *testing.T) {
	ts := newTestServer(t)
	manager := newManager(t, newAuth("claude-1", "claude", map[string]any{"access_token": testClaudeToken}))
	p, _ := newTestPoller(t, ts, manager)

	ctx, cancel := context.WithCancel(context.Background())
	waits := make(chan time.Duration, 8)
	fire := make(chan time.Time)
	p.newTimer = func(d time.Duration) (<-chan time.Time, func() bool) {
		waits <- d
		return fire, func() bool { return true }
	}

	done := make(chan struct{})
	go func() {
		p.Start(ctx)
		close(done)
	}()

	// First sweep runs before any timer is armed.
	first := <-waits
	if got := ts.hits.Load(); got != 1 {
		t.Fatalf("first sweep made %d requests, want 1", got)
	}
	// Second sweep fires when the timer elapses.
	fire <- testNow
	second := <-waits
	if got := ts.hits.Load(); got != 2 {
		t.Fatalf("after timer, requests = %d, want 2", got)
	}
	for _, d := range []time.Duration{first, second} {
		lo, hi := p.interval*9/10, p.interval*11/10
		if d < lo || d > hi {
			t.Errorf("wait %v outside %v..%v", d, lo, hi)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after ctx cancel")
	}
}

func TestDefault_NilSafe(t *testing.T) {
	SetDefault(nil)
	if Default() != nil {
		t.Fatal("Default() should be nil before SetDefault")
	}
	var nilPoller *Poller
	nilPoller.RefreshSoon("x")
	if err := nilPoller.Refresh(context.Background(), "x"); err == nil {
		t.Fatal("nil poller Refresh should error")
	}

	ts := newTestServer(t)
	p, _ := newTestPoller(t, ts, newManager(t))
	SetDefault(p)
	t.Cleanup(func() { SetDefault(nil) })
	if Default() != p {
		t.Fatal("Default() should return the poller set")
	}
}

func TestNew_Defaults(t *testing.T) {
	p := New(Config{})
	if p.interval != 5*time.Minute {
		t.Errorf("interval = %v, want 5m", p.interval)
	}
	if p.endpoints != DefaultEndpoints() {
		t.Errorf("endpoints = %+v, want defaults", p.endpoints)
	}
	if p.endpoints.ClaudeUsage != "https://api.anthropic.com/api/oauth/usage" ||
		p.endpoints.CodexUsage != "https://chatgpt.com/backend-api/wham/usage" ||
		p.endpoints.CodexCredits != "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits" {
		t.Errorf("default endpoints = %+v", p.endpoints)
	}
	client := p.client(nil)
	if client == nil || client.Timeout != 20*time.Second {
		t.Errorf("default client = %+v, want 20s timeout", client)
	}
}

func TestTokenValueForAuth(t *testing.T) {
	cases := []struct {
		name string
		auth *coreauth.Auth
		want string
	}{
		{"nil", nil, ""},
		{"access_token", &coreauth.Auth{Metadata: map[string]any{"access_token": " a "}}, "a"},
		{"accessToken first", &coreauth.Auth{Metadata: map[string]any{"accessToken": "camel", "access_token": "snake"}}, "camel"},
		{"nested token map", &coreauth.Auth{Metadata: map[string]any{"token": map[string]any{"access_token": "nested"}}}, "nested"},
		{"attributes api_key", &coreauth.Auth{Attributes: map[string]string{"api_key": "key"}}, "key"},
		{"metadata before attributes", &coreauth.Auth{Metadata: map[string]any{"id_token": "idt"}, Attributes: map[string]string{"api_key": "key"}}, "idt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TokenValueForAuth(tc.auth); got != tc.want {
				t.Errorf("TokenValueForAuth = %q, want %q", got, tc.want)
			}
		})
	}
}
