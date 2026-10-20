package cliproxy

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/quotapoll"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
)

const defaultQuotaPollInterval = 5 * time.Minute

// quotaPollInterval reads routing.reset-aware.poll-interval, default 5m.
func quotaPollInterval(cfg *config.Config) time.Duration {
	if cfg != nil {
		if interval, ok := parseRoutingDuration(cfg.Routing.ResetAware.PollInterval); ok {
			return interval
		}
	}
	return defaultQuotaPollInterval
}

// quotaPollKey summarizes the config the poller depends on; a change restarts it.
func quotaPollKey(cfg *config.Config) string {
	if cfg == nil || cfg.Home.Enabled {
		return ""
	}
	if normalizedRoutingRuntimeState(cfg).strategy != routingStrategyResetAware {
		return ""
	}
	return "interval=" + quotaPollInterval(cfg).String() + ";proxy=" + strings.TrimSpace(cfg.ProxyURL)
}

// syncQuotaPoller starts, restarts or stops the quota poller to match cfg.
// It runs only once Run has published the service lifetime context; config
// updates applied before Run are picked up when Run calls it.
func (s *Service) syncQuotaPoller(cfg *config.Config) {
	if s == nil || s.coreManager == nil {
		return
	}
	key := quotaPollKey(cfg)
	s.cfgMu.RLock()
	lifetime := s.antigravityContext
	s.cfgMu.RUnlock()

	s.quotaPollMu.Lock()
	defer s.quotaPollMu.Unlock()
	if key == s.quotaPollKey && (key == "" || s.quotaPollCancel != nil) {
		return
	}
	s.stopQuotaPollerLocked()
	s.quotaPollKey = key
	if key == "" || lifetime == nil || lifetime.Err() != nil {
		return
	}

	ctx, cancel := context.WithCancel(lifetime)
	interval := quotaPollInterval(cfg)
	poller := quotapoll.New(quotapoll.Config{
		Manager:  s.coreManager,
		Store:    coreauth.DefaultQuotaSnapshots(),
		Interval: interval,
		Client:   quotaPollClientFactory(strings.TrimSpace(cfg.ProxyURL)),
	})
	s.quotaPollCancel = cancel
	quotapoll.SetDefault(poller)
	go poller.Start(ctx)
	log.Infof("reset-aware quota poller started (interval=%s)", interval)
}

// stopQuotaPoller halts the poller and clears the default accessor.
func (s *Service) stopQuotaPoller() {
	if s == nil {
		return
	}
	s.quotaPollMu.Lock()
	defer s.quotaPollMu.Unlock()
	s.stopQuotaPollerLocked()
	s.quotaPollKey = ""
}

func (s *Service) stopQuotaPollerLocked() {
	if s.quotaPollCancel != nil {
		s.quotaPollCancel()
		s.quotaPollCancel = nil
		quotapoll.SetDefault(nil)
		log.Info("reset-aware quota poller stopped")
	}
}

// quotaPollClientFactory mirrors the management APICall proxy order: the
// auth's own proxy-url, then the global proxy-url, else a direct connection.
// Per-API-key proxy entries from config are not consulted. Clients are
// cached by effective proxy so transports and their keep-alive pools are
// reused across fetches instead of rebuilt every time.
func quotaPollClientFactory(globalProxy string) func(*coreauth.Auth) *http.Client {
	globalProxy = strings.TrimSpace(globalProxy)
	var mu sync.Mutex
	clients := make(map[string]*http.Client)
	return func(auth *coreauth.Auth) *http.Client {
		proxyStr := globalProxy
		if auth != nil {
			if own := strings.TrimSpace(auth.ProxyURL); own != "" {
				proxyStr = own
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if client, ok := clients[proxyStr]; ok {
			return client
		}
		client := newQuotaPollClient(proxyStr, globalProxy)
		clients[proxyStr] = client
		return client
	}
}

// newQuotaPollClient builds the client for one effective proxy, falling back
// to the global proxy when a per-auth value does not parse, then to direct.
func newQuotaPollClient(proxyStr, globalProxy string) *http.Client {
	client := &http.Client{Timeout: 20 * time.Second}
	candidates := []string{proxyStr}
	if globalProxy != "" && globalProxy != proxyStr {
		candidates = append(candidates, globalProxy)
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		transport, _, errBuild := proxyutil.BuildHTTPTransport(candidate)
		if errBuild != nil {
			log.WithError(errBuild).Debug("quota poller: build proxy transport failed")
			continue
		}
		if transport != nil {
			client.Transport = transport
			return client
		}
	}
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		direct := base.Clone()
		direct.Proxy = nil
		client.Transport = direct
	}
	return client
}

// quotaRefreshHook asks the running poller to re-fetch an account right after
// an upstream 429 so the reset-aware selector stops picking it.
type quotaRefreshHook struct {
	coreauth.NoopHook
}

func (quotaRefreshHook) OnResult(_ context.Context, result coreauth.Result) {
	if !isQuotaRefreshResult(result) {
		return
	}
	if poller := quotapoll.Default(); poller != nil {
		poller.RefreshSoon(result.AuthID)
	}
}

func isQuotaRefreshResult(result coreauth.Result) bool {
	if result.Success || result.Error == nil || result.AuthID == "" {
		return false
	}
	if result.Error.StatusCode() != http.StatusTooManyRequests {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(result.Provider)) {
	case "claude", "codex":
		return true
	}
	return false
}
