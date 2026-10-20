package cliproxy

import (
	"sort"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

const (
	routingStrategyResetAware      = "reset-aware"
	defaultResetAwareUrgencyWindow = 24 * time.Hour
)

// defaultResetAwareProviderTTL is the per-provider idle TTL applied by the
// reset-aware strategy when session-affinity-ttl-by-provider is unset: the
// lifetime of each provider's prompt cache.
var defaultResetAwareProviderTTL = map[string]time.Duration{
	"claude": time.Hour,
	"codex":  10 * time.Minute,
}

// parseRoutingDuration parses a config duration, returning ok=false for empty
// or invalid values and flooring positive results at one second.
func parseRoutingDuration(raw string) (time.Duration, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	parsed, errParse := time.ParseDuration(raw)
	if errParse != nil || parsed <= 0 {
		return 0, false
	}
	if parsed < time.Second {
		parsed = time.Second
	}
	return parsed, true
}

// providerTTLState renders the per-provider TTLs as a canonical string so
// routingRuntimeState stays comparable. Explicit entries override defaults.
func providerTTLState(cfg *config.Config, strategy string) string {
	ttls := make(map[string]time.Duration)
	if strategy == routingStrategyResetAware {
		for provider, ttl := range defaultResetAwareProviderTTL {
			ttls[provider] = ttl
		}
	}
	if cfg != nil {
		for provider, raw := range cfg.Routing.SessionAffinityTTLByProvider {
			provider = strings.ToLower(strings.TrimSpace(provider))
			if provider == "" {
				continue
			}
			if ttl, ok := parseRoutingDuration(raw); ok {
				ttls[provider] = ttl
			}
		}
	}
	if len(ttls) == 0 {
		return ""
	}
	providers := make([]string, 0, len(ttls))
	for provider := range ttls {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	parts := make([]string, 0, len(providers))
	for _, provider := range providers {
		parts = append(parts, provider+"="+ttls[provider].String())
	}
	return strings.Join(parts, ",")
}

// providerTTLFromState parses the canonical string back into a map.
func providerTTLFromState(state string) map[string]time.Duration {
	if state == "" {
		return nil
	}
	out := make(map[string]time.Duration)
	for _, part := range strings.Split(state, ",") {
		provider, raw, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		ttl, errParse := time.ParseDuration(raw)
		if errParse != nil || ttl <= 0 {
			continue
		}
		out[provider] = ttl
	}
	return out
}

func resetAwareUrgencyWindow(cfg *config.Config) time.Duration {
	if cfg != nil {
		if window, ok := parseRoutingDuration(cfg.Routing.ResetAware.UrgencyWindow); ok {
			return window
		}
	}
	return defaultResetAwareUrgencyWindow
}

func newResetAwareRoutingSelector(state routingRuntimeState) coreauth.Selector {
	return coreauth.NewResetAwareSelector(coreauth.ResetAwareConfig{
		Store:         coreauth.DefaultQuotaSnapshots(),
		UrgencyWindow: state.urgencyWindow,
	})
}
