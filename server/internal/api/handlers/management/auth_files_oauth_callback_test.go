package management

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestManagementCallbackURLUsesBoundHost(t *testing.T) {
	cases := []struct {
		host string
		want string
	}{
		{host: "", want: "http://127.0.0.1:8317/anthropic/callback"},
		{host: "0.0.0.0", want: "http://127.0.0.1:8317/anthropic/callback"},
		{host: "::", want: "http://127.0.0.1:8317/anthropic/callback"},
		{host: "100.84.167.41", want: "http://100.84.167.41:8317/anthropic/callback"},
		{host: "fd7a:115c:a1e0::1", want: "http://[fd7a:115c:a1e0::1]:8317/anthropic/callback"},
	}
	for _, tc := range cases {
		h := NewHandlerWithoutConfigFilePath(&config.Config{Host: tc.host, Port: 8317}, nil)
		got, errURL := h.managementCallbackURL("/anthropic/callback")
		if errURL != nil {
			t.Fatalf("host %q: unexpected error: %v", tc.host, errURL)
		}
		if got != tc.want {
			t.Errorf("host %q: got %q, want %q", tc.host, got, tc.want)
		}
	}
}
