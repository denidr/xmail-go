// Package api implements the REST API layer for xmail: routing,
// middleware, and HTTP handlers that delegate to internal/account and
// internal/mailer. See PLAN.md §3.
package api

import (
	"net/http"
	"strings"

	"xmail/internal/account"
)

// Server wires the HTTP router and its dependencies.
type Server struct {
	mux        *http.ServeMux
	apiKey     string
	service    *account.Service
	mcpHandler http.Handler
	dashboard  http.Handler
}

// NewServer builds a Server ready to ListenAndServe, with all routes
// from PLAN.md §3 registered. mcpHandler is optional (nil skips
// mounting it) — see internal/mcpserver.Server.HTTPHandler, wired in
// by internal/app so the MCP server (Fase 5) shares this same HTTP
// server, port, and API-key auth instead of needing a separate one.
//
// dashboardHandler is also optional (nil keeps the previous behavior:
// every path goes through the API-key middleware). When set, it serves
// the web dashboard's static assets for every path that is not part of
// the API — see isAPIPath and PLAN-DASHBOARD.md §2.2.
func NewServer(apiKey string, service *account.Service, mcpHandler, dashboardHandler http.Handler) *Server {
	s := &Server{
		mux:        http.NewServeMux(),
		apiKey:     apiKey,
		service:    service,
		mcpHandler: mcpHandler,
		dashboard:  dashboardHandler,
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		// Same {data,error} envelope as every other route (PRD.MD §7);
		// only the auth exemption is special (see Handler).
		writeData(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	s.mux.HandleFunc("POST /accounts", s.handleAccountsCreate)
	s.mux.HandleFunc("GET /accounts", s.handleAccountsList)
	s.mux.HandleFunc("GET /accounts/{id}", s.handleAccountGet)
	s.mux.HandleFunc("PUT /accounts/{id}", s.handleAccountUpdate)
	s.mux.HandleFunc("DELETE /accounts/{id}", s.handleAccountDelete)
	s.mux.HandleFunc("POST /accounts/{id}/test-connection", s.handleTestConnection)
	s.mux.HandleFunc("POST /accounts/{id}/send", s.handleSend)
	s.mux.HandleFunc("GET /accounts/{id}/folders", s.handleFoldersList)
	s.mux.HandleFunc("GET /accounts/{id}/messages", s.handleMessagesList)
	s.mux.HandleFunc("POST /accounts/{id}/check", s.handleCheck)
	s.mux.HandleFunc("POST /accounts/{id}/messages/read", s.handleMarkRead)

	if s.mcpHandler != nil {
		s.mux.Handle("/mcp", s.mcpHandler)
	}
}

// Handler returns the http.Handler to pass to http.Server, with
// logging and API-key auth middleware applied. /healthz is exempt
// from auth so container orchestrators can probe it without a key.
//
// When a dashboard handler is configured, it serves every non-API path
// *outside* apiKeyAuth: a browser cannot attach a header to the
// HTML/CSS/JS requests it makes to load a page, and those assets hold
// no secrets. The dashboard's own JavaScript then calls the API, where
// the key is still required.
func (s *Server) Handler() http.Handler {
	api := apiKeyAuth(s.apiKey, s.mux)
	if s.dashboard == nil {
		return requestLogger(api)
	}
	return requestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			api.ServeHTTP(w, r)
			return
		}
		s.dashboard.ServeHTTP(w, r)
	}))
}

// isAPIPath is the single place that knows which paths belong to the
// API (and therefore require X-API-Key). Everything else is served by
// the dashboard's static assets.
func isAPIPath(path string) bool {
	return path == "/healthz" || strings.HasPrefix(path, "/accounts") || strings.HasPrefix(path, "/mcp")
}
