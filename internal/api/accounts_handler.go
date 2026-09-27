package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"xmail/internal/account"
)

// writeErrFor maps a domain error to the right HTTP status + envelope.
func writeErrFor(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, account.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, account.ErrValidation):
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
	}
}

func decodeJSON[T any](r *http.Request) (T, error) {
	var v T
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&v)
	return v, err
}

func (s *Server) handleAccountsCreate(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[accountRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	password := ""
	if req.Password != nil {
		password = *req.Password
	}
	created, err := s.service.Create(r.Context(), req.toDomain(""), password)
	if err != nil {
		writeErrFor(w, err)
		return
	}
	writeData(w, http.StatusCreated, accountResponseFromDomain(created))
}

func (s *Server) handleAccountsList(w http.ResponseWriter, r *http.Request) {
	list, err := s.service.List(r.Context())
	if err != nil {
		writeErrFor(w, err)
		return
	}
	out := make([]accountResponse, 0, len(list))
	for _, a := range list {
		out = append(out, accountResponseFromDomain(a))
	}
	writeData(w, http.StatusOK, out)
}

func (s *Server) handleAccountGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	a, err := s.service.Get(r.Context(), id)
	if err != nil {
		writeErrFor(w, err)
		return
	}
	writeData(w, http.StatusOK, accountResponseFromDomain(a))
}

func (s *Server) handleAccountUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := decodeJSON[accountRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	updated, err := s.service.Update(r.Context(), req.toDomain(id), req.Password)
	if err != nil {
		writeErrFor(w, err)
		return
	}
	writeData(w, http.StatusOK, accountResponseFromDomain(updated))
}

// handleAccountDelete implements DELETE /accounts/{id}. Returns 200
// with the standard {data,error} envelope rather than 204 No Content —
// every endpoint shares that "all responses: {data, error}" contract
// with no carve-out for delete, and a bare 204 broke it for any client
// that always parses the envelope.
func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.service.Delete(r.Context(), id); err != nil {
		writeErrFor(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]bool{"deleted": true})
}

type testConnectionRequest struct {
	Protocol string `json:"protocol"`
}

func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, err := decodeJSON[testConnectionRequest](r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := s.service.TestConnection(r.Context(), id, req.Protocol); err != nil {
		writeErrFor(w, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"ok": true})
}
