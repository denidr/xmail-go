package mcpserver

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"xmail/internal/account"
	"xmail/internal/mailer"
	"xmail/internal/storage"
)

func newTestServer(t *testing.T) (*Server, *account.Service) {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "xmail.db"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	repo := account.NewRepository(db, bytes.Repeat([]byte{0x55}, 32))
	svc := account.NewService(repo)
	return New(svc, "test"), svc
}

func sampleAccount() account.Account {
	return account.Account{
		Name:     "Test",
		Email:    "test@example.com",
		Username: "test@example.com",
		SMTP:     &account.ConnectionConfig{Host: "smtp.example.com", Port: 587, TLSMode: account.TLSModeStartTLS},
		IMAP:     &account.ConnectionConfig{Host: "imap.example.com", Port: 993, TLSMode: account.TLSModeTLS},
	}
}

// mockSender/mockFetcherChecker are test doubles standing in for real
// smtp/imap clients, matching the pattern used in
// internal/account/*_test.go — MCP tool handlers should behave
// identically to the REST API since both go through account.Service.
type mockSender struct{ sent *mailer.OutgoingMessage }

func (m *mockSender) Send(ctx context.Context, msg mailer.OutgoingMessage) error {
	*m.sent = msg
	return nil
}
func (m *mockSender) TestConnection(ctx context.Context) error { return nil }

type mockFetcherChecker struct {
	fetchResult []mailer.Message
	unread      int
}

func (m mockFetcherChecker) Fetch(ctx context.Context, folder string, limit, offset int) ([]mailer.Message, error) {
	return m.fetchResult, nil
}
func (m mockFetcherChecker) TestConnection(ctx context.Context) error { return nil }
func (m mockFetcherChecker) Check(ctx context.Context, folder string) (int, error) {
	return m.unread, nil
}

func TestHandleListAccounts(t *testing.T) {
	ctx := context.Background()
	s, svc := newTestServer(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := s.handleListAccounts(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleListAccounts() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("handleListAccounts() IsError = true, content = %+v", result.Content)
	}
	list, ok := result.StructuredContent.([]accountSummary)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want []accountSummary", result.StructuredContent)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("list = %+v, want one entry with id %q", list, created.ID)
	}
	// Never leak credentials or connection config via MCP.
	if list[0].Email != created.Email {
		t.Errorf("email = %q, want %q", list[0].Email, created.Email)
	}
}

func TestHandleSendEmail(t *testing.T) {
	ctx := context.Background()
	s, svc := newTestServer(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var sent mailer.OutgoingMessage
	svc.SetSMTPSender(func(cfg account.ConnectionConfig, fromAddress, username, secret string) mailer.Sender {
		return &mockSender{sent: &sent}
	})

	result, err := s.handleSendEmail(ctx, mcp.CallToolRequest{}, sendEmailArgs{
		AccountID: created.ID,
		To:        []string{"rcpt@example.com"},
		CC:        []string{"cc@example.com"},
		BCC:       []string{"bcc@example.com"},
		Subject:   "Hi",
		BodyText:  "hello",
	})
	if err != nil {
		t.Fatalf("handleSendEmail() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("handleSendEmail() IsError = true, content = %+v", result.Content)
	}
	if len(sent.To) != 1 || sent.To[0] != "rcpt@example.com" || sent.Subject != "Hi" {
		t.Errorf("sent message = %+v, mismatched fields", sent)
	}
	// Regression: MCP send_email used to silently drop cc/bcc that the
	// REST endpoint (sendRequest) already supported — see PLAN.md §10.6.
	if len(sent.CC) != 1 || sent.CC[0] != "cc@example.com" {
		t.Errorf("sent.CC = %v, want [cc@example.com]", sent.CC)
	}
	if len(sent.BCC) != 1 || sent.BCC[0] != "bcc@example.com" {
		t.Errorf("sent.BCC = %v, want [bcc@example.com]", sent.BCC)
	}
}

func TestHandleSendEmail_MissingRecipient(t *testing.T) {
	ctx := context.Background()
	s, svc := newTestServer(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := s.handleSendEmail(ctx, mcp.CallToolRequest{}, sendEmailArgs{AccountID: created.ID, Subject: "no to"})
	if err != nil {
		t.Fatalf("handleSendEmail() unexpected transport error = %v", err)
	}
	if !result.IsError {
		t.Error("handleSendEmail() IsError = false, want true for missing recipient")
	}
}

func TestHandleFetchEmails(t *testing.T) {
	ctx := context.Background()
	s, svc := newTestServer(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	fixture := []mailer.Message{{UID: "1", Folder: "INBOX", Subject: "Hello"}}
	svc.SetIMAPFactory(func(cfg account.ConnectionConfig, username, secret string) mailer.FetcherChecker {
		return mockFetcherChecker{fetchResult: fixture}
	})

	result, err := s.handleFetchEmails(ctx, mcp.CallToolRequest{}, fetchEmailsArgs{AccountID: created.ID})
	if err != nil {
		t.Fatalf("handleFetchEmails() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("handleFetchEmails() IsError = true, content = %+v", result.Content)
	}
	msgs, ok := result.StructuredContent.([]mailer.Message)
	if !ok || len(msgs) != 1 || msgs[0].UID != "1" {
		t.Errorf("StructuredContent = %+v (%T), want fixture", result.StructuredContent, result.StructuredContent)
	}
}

func TestHandleCheckNewEmails(t *testing.T) {
	ctx := context.Background()
	s, svc := newTestServer(t)
	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	svc.SetIMAPFactory(func(cfg account.ConnectionConfig, username, secret string) mailer.FetcherChecker {
		return mockFetcherChecker{unread: 5, fetchResult: []mailer.Message{{UID: "1", Folder: "INBOX"}}}
	})

	result, err := s.handleCheckNewEmails(ctx, mcp.CallToolRequest{}, checkNewEmailsArgs{AccountID: created.ID})
	if err != nil {
		t.Fatalf("handleCheckNewEmails() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("handleCheckNewEmails() IsError = true, content = %+v", result.Content)
	}
	counts, ok := result.StructuredContent.(map[string]int)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want map[string]int", result.StructuredContent)
	}
	if counts["unread_count"] != 5 {
		t.Errorf("unread_count = %d, want 5", counts["unread_count"])
	}
	if counts["new_count"] != 1 {
		t.Errorf("new_count = %d, want 1 (first check, nothing cached yet)", counts["new_count"])
	}
}

func TestHandleListAccounts_UnknownAccountStillListsOthers(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestServer(t)
	result, err := s.handleListAccounts(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleListAccounts() error = %v", err)
	}
	list, ok := result.StructuredContent.([]accountSummary)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want []accountSummary", result.StructuredContent)
	}
	if len(list) != 0 {
		t.Errorf("list = %+v, want empty for fresh db", list)
	}
}

func TestHandleFetchEmails_MissingAccountID(t *testing.T) {
	s, _ := newTestServer(t)
	result, err := s.handleFetchEmails(context.Background(), mcp.CallToolRequest{}, fetchEmailsArgs{})
	if err != nil {
		t.Fatalf("unexpected transport error = %v", err)
	}
	if !result.IsError {
		t.Error("IsError = false, want true for missing account_id")
	}
}
