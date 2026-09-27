package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"xmail/internal/account"
	"xmail/internal/mailer"
)

// mockFolderLister is a test double for mailer.FolderLister (see
// account.Protocol.FolderLister), used to exercise the folders endpoint
// without a real IMAP server.
type mockFolderLister struct {
	folders []mailer.Folder
	err     error
}

func (m *mockFolderLister) ListFolders(ctx context.Context) ([]mailer.Folder, error) {
	return m.folders, m.err
}

func registerFolderLister(s *Server, fn func(cfg account.ConnectionConfig, username, secret string) mailer.FolderLister) {
	s.service.RegisterProtocol(account.ProtocolIMAP, account.Protocol{FolderLister: fn})
}

// TestFoldersList_Success: GET /accounts/{id}/folders returns the
// folders with delimiter/attributes, serialized directly (no DTO).
func TestFoldersList_Success(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	id := createTestAccount(t, h)

	want := []mailer.Folder{
		{Name: "INBOX", Delimiter: "/"},
		{Name: "[Gmail]/Surat Terkirim", Delimiter: "/", Attributes: []string{`\Sent`}},
	}
	registerFolderLister(s, func(cfg account.ConnectionConfig, username, secret string) mailer.FolderLister {
		return &mockFolderLister{folders: want}
	})

	rec := doRequest(t, h, http.MethodGet, "/accounts/"+id+"/folders", testAPIKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data []mailer.Folder `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(env.Data) != 2 || env.Data[1].Name != "[Gmail]/Surat Terkirim" {
		t.Fatalf("data = %+v, want %+v", env.Data, want)
	}
	if len(env.Data[1].Attributes) != 1 || env.Data[1].Attributes[0] != `\Sent` {
		t.Errorf("attributes = %v, want [\\Sent]", env.Data[1].Attributes)
	}
}

// TestFoldersList_EmptyIsArray: an empty list must serialize as [], not
// null (same contract as /messages).
func TestFoldersList_EmptyIsArray(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	id := createTestAccount(t, h)

	registerFolderLister(s, func(cfg account.ConnectionConfig, username, secret string) mailer.FolderLister {
		return &mockFolderLister{} // returns nil
	})

	rec := doRequest(t, h, http.MethodGet, "/accounts/"+id+"/folders", testAPIKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(env.Data) != "[]" {
		t.Errorf("data = %s, want [] (an empty folder list must not serialize as null)", env.Data)
	}
}

// TestFoldersList_ProtocolWithoutFolders: POP3 is a caller error (400),
// not a server error (PLAN-FOLDERS.md §2).
func TestFoldersList_ProtocolWithoutFolders(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	id := createTestAccount(t, h)

	rec := doRequest(t, h, http.MethodGet, "/accounts/"+id+"/folders?protocol=pop3", testAPIKey, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (pop3 has no folders), body = %s", rec.Code, rec.Body.String())
	}
}

func TestFoldersList_UnknownAccountIs404(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	rec := doRequest(t, h, http.MethodGet, "/accounts/00000000-0000-0000-0000-000000000000/folders", testAPIKey, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}
}

// TestMessagesList_FolderNotFoundIs400 is the REST proof of the
// deliberate behavior change (PLAN-FOLDERS.md §3.8): a caller-supplied
// folder that does not exist now answers 400 validation_failed, where
// it used to be 500 internal_error. It fails without account.domainError.
func TestMessagesList_FolderNotFoundIs400(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	id := createTestAccount(t, h)

	registerIMAP(s, func(cfg account.ConnectionConfig, username, secret string) mailer.Fetcher {
		return &mockIMAPClient{fetchErr: fmt.Errorf("imap: select %q: %w", "Nope", mailer.ErrFolderNotFound)}
	})

	rec := doRequest(t, h, http.MethodGet, "/accounts/"+id+"/messages?folder=Nope", testAPIKey, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (folder not found), body = %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if env.Error.Code != "validation_failed" {
		t.Errorf("error.code = %q, want validation_failed", env.Error.Code)
	}
}

// TestMessagesList_NetworkErrorStays500: the classification must not
// over-reach — a real server problem is still a 500.
func TestMessagesList_NetworkErrorStays500(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	id := createTestAccount(t, h)

	registerIMAP(s, func(cfg account.ConnectionConfig, username, secret string) mailer.Fetcher {
		return &mockIMAPClient{fetchErr: errors.New("imap: connect: dial tcp: connection refused")}
	})

	rec := doRequest(t, h, http.MethodGet, "/accounts/"+id+"/messages", testAPIKey, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (a dial failure is a server error), body = %s", rec.Code, rec.Body.String())
	}
}
