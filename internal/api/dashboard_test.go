package api

import (
	"net/http"
	"testing"

	"xmail/internal/dashboard"
)

// dashboardServer builds the Server exactly like internal/app does: the
// real embedded dashboard handler, mounted alongside the API.
func dashboardServer(t *testing.T) *Server {
	t.Helper()
	return newTestServerWith(t, dashboard.Handler())
}

func TestDashboard_AssetsServedWithoutAPIKey(t *testing.T) {
	h := dashboardServer(t).Handler()

	for _, path := range []string{"/", "/app.js", "/style.css"} {
		rec := doRequest(t, h, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s without key: status = %d, want 200", path, rec.Code)
			continue
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s returned an empty body", path)
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != "default-src 'self'" {
			t.Errorf("GET %s: Content-Security-Policy = %q, want %q", path, got, "default-src 'self'")
		}
	}
}

// TestDashboard_APIStillRequiresKey is the guard that matters: mounting
// the dashboard must not make any API endpoint public.
func TestDashboard_APIStillRequiresKey(t *testing.T) {
	h := dashboardServer(t).Handler()

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/accounts"},
		{http.MethodPost, "/accounts"},
		{http.MethodGet, "/accounts/some-id"},
		{http.MethodPut, "/accounts/some-id"},
		{http.MethodDelete, "/accounts/some-id"},
		{http.MethodPost, "/accounts/some-id/test-connection"},
		{http.MethodPost, "/mcp"},
	} {
		rec := doRequest(t, h, tc.method, tc.path, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without key: status = %d, want 401", tc.method, tc.path, rec.Code)
		}
	}

	rec := doRequest(t, h, http.MethodGet, "/accounts", testAPIKey, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("GET /accounts with key: status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
}

// TestDashboard_HeadersDoNotLeakOntoAPI: the dashboard's CSP (and its
// no-auth mount point) belong to the static assets only.
func TestDashboard_HeadersDoNotLeakOntoAPI(t *testing.T) {
	h := dashboardServer(t).Handler()

	rec := doRequest(t, h, http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz: status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "" {
		t.Errorf("API response carries the dashboard CSP %q", got)
	}
}

// TestDashboard_UnknownPathIs404: non-API, non-asset paths fall through
// to the file server rather than reaching the API.
func TestDashboard_UnknownPathIs404(t *testing.T) {
	rec := doRequest(t, dashboardServer(t).Handler(), http.MethodGet, "/nope", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /nope: status = %d, want 404", rec.Code)
	}
}

// TestDashboard_NilHandlerKeepsPreviousBehavior: with no dashboard
// wired (library/test usage), every path still goes through apiKeyAuth.
func TestDashboard_NilHandlerKeepsPreviousBehavior(t *testing.T) {
	h := newTestServer(t).Handler() // nil dashboard
	rec := doRequest(t, h, http.MethodGet, "/", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("GET / with no dashboard: status = %d, want 401", rec.Code)
	}
}
