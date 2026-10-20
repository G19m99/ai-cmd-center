package cliproxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/quotapoll"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func resetAwareConfig(pollInterval string) *config.Config {
	return &config.Config{
		Routing: internalconfig.RoutingConfig{
			Strategy:   "reset-aware",
			ResetAware: internalconfig.ResetAwareRoutingConfig{PollInterval: pollInterval},
		},
	}
}

func TestQuotaPollInterval(t *testing.T) {
	if got := quotaPollInterval(nil); got != 5*time.Minute {
		t.Fatalf("nil config interval = %v, want 5m", got)
	}
	if got := quotaPollInterval(resetAwareConfig("")); got != 5*time.Minute {
		t.Fatalf("empty interval = %v, want 5m", got)
	}
	if got := quotaPollInterval(resetAwareConfig("90s")); got != 90*time.Second {
		t.Fatalf("90s interval = %v", got)
	}
	if got := quotaPollInterval(resetAwareConfig("bogus")); got != 5*time.Minute {
		t.Fatalf("invalid interval = %v, want 5m", got)
	}
}

func TestQuotaPollKey_OnlyForResetAware(t *testing.T) {
	if key := quotaPollKey(&config.Config{Routing: internalconfig.RoutingConfig{Strategy: "round-robin"}}); key != "" {
		t.Fatalf("round-robin key = %q, want empty", key)
	}
	home := resetAwareConfig("1m")
	home.Home.Enabled = true
	if key := quotaPollKey(home); key != "" {
		t.Fatalf("home mode key = %q, want empty", key)
	}
	a := quotaPollKey(resetAwareConfig("1m"))
	b := quotaPollKey(resetAwareConfig("2m"))
	if a == "" || b == "" || a == b {
		t.Fatalf("reset-aware keys should be non-empty and interval-sensitive: %q vs %q", a, b)
	}
}

func TestSyncQuotaPoller_FollowsStrategyAndLifetime(t *testing.T) {
	t.Cleanup(func() { quotapoll.SetDefault(nil) })
	svc := &Service{cfg: resetAwareConfig("1m"), coreManager: coreauth.NewManager(nil, nil, nil)}

	// Before Run publishes a lifetime context nothing starts.
	svc.syncQuotaPoller(svc.cfg)
	if quotapoll.Default() != nil || svc.quotaPollCancel != nil {
		t.Fatal("poller must not start without a lifetime context")
	}

	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.cfgMu.Lock()
	svc.antigravityContext = lifetime
	svc.cfgMu.Unlock()

	svc.syncQuotaPoller(svc.cfg)
	first := quotapoll.Default()
	if first == nil || svc.quotaPollCancel == nil {
		t.Fatal("poller should start for reset-aware strategy once Run is active")
	}

	// Same config is a no-op.
	svc.syncQuotaPoller(resetAwareConfig("1m"))
	if quotapoll.Default() != first {
		t.Fatal("unchanged config should keep the same poller")
	}

	// Interval change restarts.
	svc.syncQuotaPoller(resetAwareConfig("2m"))
	second := quotapoll.Default()
	if second == nil || second == first {
		t.Fatal("interval change should restart the poller")
	}

	// Switching away from reset-aware stops it.
	svc.syncQuotaPoller(&config.Config{Routing: internalconfig.RoutingConfig{Strategy: "round-robin"}})
	if quotapoll.Default() != nil || svc.quotaPollCancel != nil {
		t.Fatal("poller should stop when the strategy is not reset-aware")
	}

	// Switching back starts again; stopQuotaPoller clears it for shutdown.
	svc.syncQuotaPoller(resetAwareConfig("1m"))
	if quotapoll.Default() == nil {
		t.Fatal("poller should restart when reset-aware returns")
	}
	svc.stopQuotaPoller()
	if quotapoll.Default() != nil || svc.quotaPollCancel != nil || svc.quotaPollKey != "" {
		t.Fatal("stopQuotaPoller should clear the poller state")
	}
}

func TestQuotaRefreshHook_Only429ForClaudeAndCodex(t *testing.T) {
	cases := []struct {
		name   string
		result coreauth.Result
		want   bool
	}{
		{"claude 429", coreauth.Result{AuthID: "a", Provider: "claude", Error: &coreauth.Error{HTTPStatus: http.StatusTooManyRequests}}, true},
		{"codex 429", coreauth.Result{AuthID: "a", Provider: "Codex", Error: &coreauth.Error{HTTPStatus: http.StatusTooManyRequests}}, true},
		{"gemini 429", coreauth.Result{AuthID: "a", Provider: "gemini", Error: &coreauth.Error{HTTPStatus: http.StatusTooManyRequests}}, false},
		{"claude 500", coreauth.Result{AuthID: "a", Provider: "claude", Error: &coreauth.Error{HTTPStatus: http.StatusInternalServerError}}, false},
		{"claude success", coreauth.Result{AuthID: "a", Provider: "claude", Success: true}, false},
		{"claude no error", coreauth.Result{AuthID: "a", Provider: "claude"}, false},
		{"missing auth id", coreauth.Result{Provider: "claude", Error: &coreauth.Error{HTTPStatus: http.StatusTooManyRequests}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isQuotaRefreshResult(tc.result); got != tc.want {
				t.Fatalf("isQuotaRefreshResult = %v, want %v", got, tc.want)
			}
		})
	}
	// With no poller running the hook must be a safe no-op.
	quotapoll.SetDefault(nil)
	quotaRefreshHook{}.OnResult(context.Background(), cases[0].result)
}

func TestQuotaPollClientFactory(t *testing.T) {
	client := quotaPollClientFactory("")(&coreauth.Auth{ID: "a"})
	if client == nil || client.Timeout != 20*time.Second {
		t.Fatalf("client = %+v, want 20s timeout", client)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatalf("direct client should have a transport with no proxy, got %T", client.Transport)
	}

	proxied := quotaPollClientFactory("http://127.0.0.1:9")(&coreauth.Auth{ID: "a"})
	transport, ok = proxied.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil {
		t.Fatalf("global proxy should be honoured, got %T", proxied.Transport)
	}

	perAuth := quotaPollClientFactory("")(&coreauth.Auth{ID: "a", ProxyURL: "socks5://127.0.0.1:9"})
	if perAuth.Transport == nil {
		t.Fatal("per-auth proxy should produce a transport")
	}
}
