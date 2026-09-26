// Package api implements the REST API layer for xmail: routing,
// middleware, and HTTP handlers that delegate to internal/account and
// internal/mailer. See PLAN.md §3.
package api

import (
	"net/http"

	"xmail/internal/account"
)

// Server wires the HTTP router and its dependencies.
type Server struct {
	mux        *http.ServeMux
	apiKey     string
	service    *account.Service
	mcpHandler http.Handler
}

// NewServer builds a Server ready to ListenAndServe, with all routes
// from PLAN.md §3 registered. mcpHandler is optional (nil skips
// mounting it) — see internal/mcpserver.Server.HTTPHandler, wired in
// by internal/app so the MCP server (Fase 5) shares this same HTTP
// server, port, and API-key auth instead of needing a separate one.
func NewServer(apiKey string, service *account.Service, mcpHandler http.Handler) *Server {
	s := &Server{
		mux:        http.NewServeMux(),
		apiKey:     apiKey,
		service:    service,
		mcpHandler: mcpHandler,
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	s.mux.HandleFunc("POST /accounts", s.handleAccountsCreate)
	s.mux.HandleFunc("GET /accounts", s.handleAccountsList)
	s.mux.HandleFunc("GET /accounts/{id}", s.handleAccountGet)
	s.mux.HandleFunc("PUT /accounts/{id}", s.handleAccountUpdate)
	s.mux.HandleFunc("DELETE /accounts/{id}", s.handleAccountDelete)
	s.mux.HandleFunc("POST /accounts/{id}/test-connection", s.handleTestConnection)
	s.mux.HandleFunc("POST /accounts/{id}/send", s.handleSend)
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
func (s *Server) Handler() http.Handler {
	return requestLogger(apiKeyAuth(s.apiKey, s.mux))
}
