package api

import (
	"net/http"
	"strconv"

	"xmail/internal/account"
	"xmail/internal/mailer"
)

func queryIntOr(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func queryBool(r *http.Request, key string) bool {
	v := r.URL.Query().Get(key)
	return v == "1" || v == "true"
}

// handleMessagesList implements GET /accounts/{id}/messages (see
// PLAN.md §3): ?folder=INBOX&limit=20&offset=0&protocol=imap&refresh=true
//
// folder/protocol/limit are all passed through as-is (including empty
// string / 0) and defaulted once, centrally, in account.Service — see
// CODE_REVIEW.md "Duplicated Code" (defaults used to be reimplemented
// per-handler, and REST's explicit limit=0 used to disagree with
// MCP's).
func (s *Server) handleMessagesList(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	folder := r.URL.Query().Get("folder")
	protocol := r.URL.Query().Get("protocol")
	limit := queryIntOr(r, "limit", 0)
	offset := queryIntOr(r, "offset", 0)
	refresh := queryBool(r, "refresh")

	msgs, err := s.service.FetchMessages(r.Context(), id, protocol, folder, limit, offset, refresh)
	if err != nil {
		writeErrFor(w, err)
		return
	}
	// An empty mailbox must serialize as [] , not null — a client that
	// iterates data would break only on that one day. The account list
	// normalizes the same way (see handleList's make(..., 0, len)).
	if msgs == nil {
		msgs = []mailer.Message{}
	}
	writeData(w, http.StatusOK, msgs)
}

type checkRequest struct {
	Protocol string `json:"protocol"`
	Folder   string `json:"folder"`
}

// handleCheck implements POST /accounts/{id}/check (see PLAN.md §3).
func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := decodeJSON[checkRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}

	unread, newCount, err := s.service.CheckNew(r.Context(), id, req.Protocol, req.Folder)
	if err != nil {
		writeErrFor(w, err)
		return
	}
	// The same account.CheckResult MCP's check_new_emails serializes —
	// one shared shape for both skins.
	writeData(w, http.StatusOK, account.CheckResult{Unread: unread, New: newCount})
}

type markReadRequest struct {
	Protocol string `json:"protocol"`
	Folder   string `json:"folder"`
	UID      string `json:"uid"`
}

// handleMarkRead implements POST /accounts/{id}/messages/read — marks
// one message as read (IMAP only, see mailer.Marker / PRD.MD §6.3
// "mark as read").
func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := decodeJSON[markReadRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if req.UID == "" {
		writeError(w, http.StatusBadRequest, "validation_failed", "uid is required")
		return
	}
	if err := s.service.MarkRead(r.Context(), id, req.Protocol, req.Folder, req.UID); err != nil {
		writeErrFor(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]bool{"marked_read": true})
}
