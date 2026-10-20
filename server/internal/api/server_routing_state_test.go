package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutingState_RequiresManagementAuth(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "test-management-key")
	server := newTestServer(t)

	routes := []struct{ method, path string }{
		{http.MethodGet, "/v0/management/routing-state"},
		{http.MethodPost, "/v0/management/routing-state/refresh"},
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
	req := httptest.NewRequest(http.MethodGet, "/v0/management/routing-state", nil)
	req.Header.Set("Authorization", "Bearer test-management-key")
	server.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET routing-state with key = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}
