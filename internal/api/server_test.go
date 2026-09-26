package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"xmail/internal/account"
	"xmail/internal/mailer"
	"xmail/internal/storage"
)

const testAPIKey = "test-api-key"

func newTestServer(t *testing.T) *Server {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "xmail.db"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	repo := account.NewRepository(db, bytes.Repeat([]byte{0x33}, 32))
	svc := account.NewService(repo)
	return NewServer(testAPIKey, svc, nil)
}

func doRequest(t *testing.T, h http.Handler, method, path, apiKey string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if apiKey != "" {
		req.Header.Set("X-API-Key", apiKey)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthz_NoAuthRequired(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodGet, "/healthz", "", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestAuth_MissingOrWrongKeyRejected(t *testing.T) {
	s := newTestServer(t)

	rec := doRequest(t, s.Handler(), http.MethodGet, "/accounts", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no key: status = %d, want 401", rec.Code)
	}

	rec = doRequest(t, s.Handler(), http.MethodGet, "/accounts", "wrong-key", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong key: status = %d, want 401", rec.Code)
	}
}

func samplePassword() *string {
	p := "s3cret"
	return &p
}

func sampleCreateReq() accountRequest {
	return accountRequest{
		Name:     "Test",
		Email:    "test@example.com",
		Username: "test@example.com",
		Password: samplePassword(),
		SMTP:     &connConfigDTO{Host: "smtp.example.com", Port: 587, TLSMode: "starttls"},
		IMAP:     &connConfigDTO{Host: "imap.example.com", Port: 993, TLSMode: "tls"},
	}
}

func TestAccountsCRUD_EndToEnd(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	// Create
	rec := doRequest(t, h, http.MethodPost, "/accounts", testAPIKey, sampleCreateReq())
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var createEnv struct {
		Data accountResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createEnv); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createEnv.Data.ID == "" {
		t.Fatal("created account has no id")
	}
	id := createEnv.Data.ID

	// Response must never leak the password field.
	if bytes.Contains(rec.Body.Bytes(), []byte("s3cret")) {
		t.Error("create response leaks plaintext password")
	}

	// Get
	rec = doRequest(t, h, http.MethodGet, "/accounts/"+id, testAPIKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// List
	rec = doRequest(t, h, http.MethodGet, "/accounts", testAPIKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status = %d", rec.Code)
	}
	var listEnv struct {
		Data []accountResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listEnv); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listEnv.Data) != 1 {
		t.Fatalf("list len = %d, want 1", len(listEnv.Data))
	}

	// Update
	updateReq := sampleCreateReq()
	updateReq.Name = "Renamed"
	updateReq.Password = nil
	rec = doRequest(t, h, http.MethodPut, "/accounts/"+id, testAPIKey, updateReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, body = %s", rec.Code, rec.Body.String())
	}

	// Delete — 200 with the standard {data,error} envelope, not a bare 204
	// (see PLAN.md §3 "Semua response: {data, error}").
	rec = doRequest(t, h, http.MethodDelete, "/accounts/"+id, testAPIKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var deleteEnv struct {
		Data  map[string]bool `json:"data"`
		Error *struct{}       `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &deleteEnv); err != nil {
		t.Fatalf("decode delete response: %v", err)
	}
	if deleteEnv.Error != nil {
		t.Errorf("delete response has non-nil error: %+v", deleteEnv.Error)
	}
	if !deleteEnv.Data["deleted"] {
		t.Errorf("delete response data = %+v, want {deleted:true}", deleteEnv.Data)
	}

	// Get after delete -> 404
	rec = doRequest(t, h, http.MethodGet, "/accounts/"+id, testAPIKey, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("get after delete: status = %d, want 404", rec.Code)
	}
}

func TestAccountsCreate_ValidationError(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodPost, "/accounts", testAPIKey, accountRequest{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
}

func TestMessagesAndCheck_NotWiredYetReturnsError(t *testing.T) {
	// account.Service has no IMAP factory wired in these API-layer
	// tests (that only happens in internal/app.wireMailer), so both
	// endpoints must surface a clear error rather than panicking or
	// silently returning an empty list.
	s := newTestServer(t)
	h := s.Handler()

	rec := doRequest(t, h, http.MethodPost, "/accounts", testAPIKey, sampleCreateReq())
	var createEnv struct {
		Data accountResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &createEnv)
	id := createEnv.Data.ID

	rec = doRequest(t, h, http.MethodGet, "/accounts/"+id+"/messages", testAPIKey, nil)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("messages: status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}

	rec = doRequest(t, h, http.MethodPost, "/accounts/"+id+"/check", testAPIKey, checkRequest{})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("check: status = %d, want 500, body = %s", rec.Code, rec.Body.String())
	}
}

func TestSend_ValidatesRecipients(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	rec := doRequest(t, h, http.MethodPost, "/accounts", testAPIKey, sampleCreateReq())
	var createEnv struct {
		Data accountResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &createEnv)

	rec = doRequest(t, h, http.MethodPost, "/accounts/"+createEnv.Data.ID+"/send", testAPIKey, sendRequest{Subject: "no recipients"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing to), body = %s", rec.Code, rec.Body.String())
	}
}

// mockIMAPClient is a test double for mailer.FetcherChecker +
// mailer.Marker, used to exercise the messages/check/mark-read
// endpoints without a real IMAP server.
type mockIMAPClient struct {
	fetchResult   []mailer.Message
	fetchCalls    int
	markReadCalls []string
}

func (m *mockIMAPClient) Fetch(ctx context.Context, folder string, limit, offset int) ([]mailer.Message, error) {
	m.fetchCalls++
	return m.fetchResult, nil
}
func (m *mockIMAPClient) TestConnection(ctx context.Context) error              { return nil }
func (m *mockIMAPClient) Check(ctx context.Context, folder string) (int, error) { return 0, nil }
func (m *mockIMAPClient) MarkRead(ctx context.Context, folder, uid string) error {
	m.markReadCalls = append(m.markReadCalls, uid)
	return nil
}

func createTestAccount(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := doRequest(t, h, http.MethodPost, "/accounts", testAPIKey, sampleCreateReq())
	var env struct {
		Data accountResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if env.Data.ID == "" {
		t.Fatalf("create account failed: %s", rec.Body.String())
	}
	return env.Data.ID
}

// TestMessagesList_Refresh proves the ?refresh=true query param
// reaches account.Service.FetchMessages and forces a live dial even
// when the cache already has results — REST-level counterpart to
// internal/account's TestService_FetchMessages_ServesFromCacheOnSecondCall
// (CODE_REVIEW.md "message cache write-only" fix).
func TestMessagesList_Refresh(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	id := createTestAccount(t, h)

	mock := &mockIMAPClient{fetchResult: []mailer.Message{{UID: "1", Folder: "INBOX", Subject: "Hi"}}}
	s.service.SetIMAPFactory(func(cfg account.ConnectionConfig, username, secret string) mailer.FetcherChecker {
		return mock
	})

	rec := doRequest(t, h, http.MethodGet, "/accounts/"+id+"/messages", testAPIKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("1st fetch: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if mock.fetchCalls != 1 {
		t.Fatalf("fetchCalls after 1st request = %d, want 1", mock.fetchCalls)
	}

	rec = doRequest(t, h, http.MethodGet, "/accounts/"+id+"/messages", testAPIKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("2nd fetch: status = %d", rec.Code)
	}
	if mock.fetchCalls != 1 {
		t.Errorf("fetchCalls after 2nd request (no refresh) = %d, want still 1 (cache hit)", mock.fetchCalls)
	}

	rec = doRequest(t, h, http.MethodGet, "/accounts/"+id+"/messages?refresh=true", testAPIKey, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh fetch: status = %d", rec.Code)
	}
	if mock.fetchCalls != 2 {
		t.Errorf("fetchCalls after ?refresh=true = %d, want 2", mock.fetchCalls)
	}
}

// TestMarkRead_Success proves POST /accounts/{id}/messages/read
// reaches the wired mailer.Marker implementation with the right
// folder/uid (PRD.MD §6.3 "mark as read", previously entirely absent
// — see CODE_REVIEW.md).
func TestMarkRead_Success(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	id := createTestAccount(t, h)

	mock := &mockIMAPClient{}
	s.service.SetIMAPFactory(func(cfg account.ConnectionConfig, username, secret string) mailer.FetcherChecker {
		return mock
	})

	rec := doRequest(t, h, http.MethodPost, "/accounts/"+id+"/messages/read", testAPIKey, markReadRequest{Protocol: "imap", Folder: "INBOX", UID: "42"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(mock.markReadCalls) != 1 || mock.markReadCalls[0] != "42" {
		t.Errorf("markReadCalls = %v, want [42]", mock.markReadCalls)
	}
}

func TestMarkRead_MissingUID(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	id := createTestAccount(t, h)

	rec := doRequest(t, h, http.MethodPost, "/accounts/"+id+"/messages/read", testAPIKey, markReadRequest{Protocol: "imap"})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing uid), body = %s", rec.Code, rec.Body.String())
	}
}

// TestSend_CustomHeaders proves the "headers" request field reaches
// mailer.OutgoingMessage.Headers (PRD.MD §6.2 "custom headers dasar").
func TestSend_CustomHeaders(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()
	id := createTestAccount(t, h)

	var captured mailer.OutgoingMessage
	s.service.SetSMTPSender(func(cfg account.ConnectionConfig, fromAddress, username, secret string) mailer.Sender {
		return &captureSender{msg: &captured}
	})

	req := sendRequest{To: []string{"rcpt@example.com"}, Subject: "hi", Headers: map[string]string{"X-Priority": "1"}}
	rec := doRequest(t, h, http.MethodPost, "/accounts/"+id+"/send", testAPIKey, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if captured.Headers["X-Priority"] != "1" {
		t.Errorf("captured Headers = %v, want X-Priority=1", captured.Headers)
	}
}

type captureSender struct{ msg *mailer.OutgoingMessage }

func (c *captureSender) Send(ctx context.Context, msg mailer.OutgoingMessage) error {
	*c.msg = msg
	return nil
}
func (c *captureSender) TestConnection(ctx context.Context) error { return nil }

func TestTestConnection_NotWiredYetReturnsError(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler()

	rec := doRequest(t, h, http.MethodPost, "/accounts", testAPIKey, sampleCreateReq())
	var createEnv struct {
		Data accountResponse `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &createEnv)

	rec = doRequest(t, h, http.MethodPost, "/accounts/"+createEnv.Data.ID+"/test-connection", testAPIKey, testConnectionRequest{Protocol: "smtp"})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (SMTP tester not wired until Fase 2), body = %s", rec.Code, rec.Body.String())
	}
}
