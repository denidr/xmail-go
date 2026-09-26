// Package dashboard serves xmail's web dashboard: a small set of
// embedded static assets (HTML/CSS/JS) that drive account management
// (CRUD + per-protocol connectivity checks) against the existing REST
// API. See PRD.MD §6.7 and PLAN-DASHBOARD.md.
//
// The assets are embedded into the binary with go:embed, so all three
// release targets (Docker x64, Docker arm64, Windows tray) serve the
// dashboard without a build step or a new dependency — see
// PLAN-DASHBOARD.md §0.1.
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets
var embedded embed.FS

// Handler returns the http.Handler that serves the dashboard's static
// assets. It is mounted by internal/api outside the X-API-Key
// middleware: a browser cannot attach a header to the HTML/CSS/JS
// requests it makes while loading a page, and the assets hold no
// secrets. Every API call the dashboard then makes from JavaScript
// still requires X-API-Key (see internal/api.Server.Handler and
// PLAN-DASHBOARD.md §2.2).
func Handler() http.Handler {
	sub, err := fs.Sub(embedded, "assets")
	if err != nil {
		// Only reachable if the //go:embed directive and this path
		// disagree — a build-time invariant. Fail loudly at startup
		// rather than serving a blank dashboard.
		panic("dashboard: embedded assets not found: " + err.Error())
	}
	return &handler{files: http.FileServerFS(sub)}
}

type handler struct {
	files http.Handler
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The dashboard keeps the API key in sessionStorage, so its own
	// document is locked to same-origin assets: no CDN, no inline
	// <script>/<style>/style="". This is the second layer behind
	// "never render server data with innerHTML" in assets/app.js.
	w.Header().Set("Content-Security-Policy", "default-src 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// Revalidate every asset (FileServerFS answers with 304 via
	// Last-Modified when unchanged). Without this, an upgraded binary
	// could pair a fresh index.html with a stale, cached app.js — the
	// classic split-version bug, and one no Go test would catch.
	w.Header().Set("Cache-Control", "no-cache")
	h.files.ServeHTTP(w, r)
}
