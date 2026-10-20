package management

import "testing"

func TestNormalizeRoutingStrategyResetAware(t *testing.T) {
	for _, input := range []string{"reset-aware", "resetaware", "ra", " Reset-Aware "} {
		got, ok := normalizeRoutingStrategy(input)
		if !ok || got != "reset-aware" {
			t.Fatalf("normalizeRoutingStrategy(%q) = %q, %v; want reset-aware, true", input, got, ok)
		}
	}
}
