package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutingState_RequiresManagementAuth(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")
	server := newTestServer(t)

	for _, prefix := range []string{"/v0/management", "/v8/management"} {
		routes := []struct{ method, path string }{
			{http.MethodGet, prefix + "/routing-state"},
			{http.MethodPost, prefix + "/routing-state/refresh"},
		}
		for _, route := range routes {
			rec := httptest.NewRecorder()
			server.engine.ServeHTTP(rec, httptest.NewRequest(route.method, route.path, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s without key = %d, want 401", route.method, route.path, rec.Code)
			}

			registered := false
			for _, info := range server.engine.Routes() {
				if info.Method == route.method && info.Path == route.path {
					registered = true
				}
			}
			if !registered {
				t.Fatalf("%s %s not registered", route.method, route.path)
			}
		}

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, prefix+"/routing-state", nil)
		req.Header.Set("Authorization", "Bearer test-management-key")
		server.engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s/routing-state with key = %d, want 200 (%s)", prefix, rec.Code, rec.Body.String())
		}
		t.Logf("GET %s/routing-state: %s", prefix, rec.Body.String())
	}
}
