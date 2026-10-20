package cliproxy

import (
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestRoutingState_ResetAwareStrategy(t *testing.T) {
	state := normalizedRoutingRuntimeState(&internalconfig.Config{
		Routing: internalconfig.RoutingConfig{Strategy: "reset-aware"},
	})
	if state.strategy != "reset-aware" {
		t.Fatalf("strategy = %q, want reset-aware", state.strategy)
	}
	if !state.sessionAffinity {
		t.Fatal("reset-aware must force session affinity on")
	}
	if state.urgencyWindow != 24*time.Hour {
		t.Fatalf("urgencyWindow = %v, want 24h", state.urgencyWindow)
	}

	selector := newRoutingSelector(state)
	affinity, ok := selector.(*coreauth.SessionAffinitySelector)
	if !ok {
		t.Fatalf("selector type = %T, want *auth.SessionAffinitySelector", selector)
	}
	defer affinity.Stop()
	if _, ok := affinity.Fallback().(*coreauth.ResetAwareSelector); !ok {
		t.Fatalf("Fallback() type = %T, want *auth.ResetAwareSelector", affinity.Fallback())
	}
	ttls := affinity.ProviderTTLs()
	if ttls["claude"] != time.Hour || ttls["codex"] != 10*time.Minute {
		t.Fatalf("ProviderTTLs() = %v, want claude=1h codex=10m", ttls)
	}
}

func TestRoutingState_ResetAwareStrategyHonoursOverrides(t *testing.T) {
	state := normalizedRoutingRuntimeState(&internalconfig.Config{
		Routing: internalconfig.RoutingConfig{
			Strategy:                     "reset-aware",
			SessionAffinityTTL:           "45m",
			SessionAffinityTTLByProvider: map[string]string{"codex": "5m"},
			ResetAware:                   internalconfig.ResetAwareRoutingConfig{UrgencyWindow: "12h"},
		},
	})
	if state.urgencyWindow != 12*time.Hour {
		t.Fatalf("urgencyWindow = %v, want 12h", state.urgencyWindow)
	}
	if state.sessionAffinityTTL != 45*time.Minute {
		t.Fatalf("sessionAffinityTTL = %v, want 45m", state.sessionAffinityTTL)
	}

	affinity, ok := newRoutingSelector(state).(*coreauth.SessionAffinitySelector)
	if !ok {
		t.Fatalf("selector type = %T, want *auth.SessionAffinitySelector", newRoutingSelector(state))
	}
	defer affinity.Stop()
	ttls := affinity.ProviderTTLs()
	if ttls["claude"] != time.Hour || ttls["codex"] != 5*time.Minute {
		t.Fatalf("ProviderTTLs() = %v, want claude=1h codex=5m", ttls)
	}
}

func TestRoutingState_ProviderTTLWithoutResetAware(t *testing.T) {
	state := normalizedRoutingRuntimeState(&internalconfig.Config{
		Routing: internalconfig.RoutingConfig{
			Strategy:                     "round-robin",
			SessionAffinity:              true,
			SessionAffinityTTLByProvider: map[string]string{"codex": "5m"},
		},
	})
	if state.strategy != "round-robin" {
		t.Fatalf("strategy = %q, want round-robin", state.strategy)
	}
	affinity, ok := newRoutingSelector(state).(*coreauth.SessionAffinitySelector)
	if !ok {
		t.Fatalf("selector type = %T, want *auth.SessionAffinitySelector", newRoutingSelector(state))
	}
	defer affinity.Stop()
	if _, ok := affinity.Fallback().(*coreauth.RoundRobinSelector); !ok {
		t.Fatalf("Fallback() type = %T, want *auth.RoundRobinSelector", affinity.Fallback())
	}
	ttls := affinity.ProviderTTLs()
	if len(ttls) != 1 || ttls["codex"] != 5*time.Minute {
		t.Fatalf("ProviderTTLs() = %v, want only codex=5m (no reset-aware defaults)", ttls)
	}
}
