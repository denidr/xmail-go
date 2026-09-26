package mcpserver

import (
	"bytes"
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	mcpclient "github.com/mark3labs/mcp-go/client"
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

func registerSender(svc *account.Service, fn func(cfg account.ConnectionConfig, fromAddress, username, secret string) mailer.Sender) {
	svc.RegisterProtocol(account.ProtocolSMTP, account.Protocol{Sender: fn})
}

func registerIMAPFetcher(svc *account.Service, fn func(cfg account.ConnectionConfig, username, secret string) mailer.Fetcher) {
	svc.RegisterProtocol(account.ProtocolIMAP, account.Protocol{Fetcher: fn})
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
	registerSender(svc, func(cfg account.ConnectionConfig, fromAddress, username, secret string) mailer.Sender {
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
	registerIMAPFetcher(svc, func(cfg account.ConnectionConfig, username, secret string) mailer.Fetcher {
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

	registerIMAPFetcher(svc, func(cfg account.ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{unread: 5, fetchResult: []mailer.Message{{UID: "1", Folder: "INBOX"}}}
	})

	result, err := s.handleCheckNewEmails(ctx, mcp.CallToolRequest{}, checkNewEmailsArgs{AccountID: created.ID})
	if err != nil {
		t.Fatalf("handleCheckNewEmails() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("handleCheckNewEmails() IsError = true, content = %+v", result.Content)
	}
	// One shared shape with REST (account.CheckResult).
	counts, ok := result.StructuredContent.(account.CheckResult)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want account.CheckResult", result.StructuredContent)
	}
	if counts.Unread != 5 {
		t.Errorf("Unread = %d, want 5", counts.Unread)
	}
	if counts.New != 1 {
		t.Errorf("New = %d, want 1 (first check, nothing cached yet)", counts.New)
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

// dialMCP starts an in-process MCP client over the real Streamable HTTP
// transport against s's handler and completes the initialize handshake.
func dialMCP(t *testing.T, s *Server) *mcpclient.Client {
	t.Helper()
	httpSrv := httptest.NewServer(s.HTTPHandler())
	t.Cleanup(httpSrv.Close)

	c, err := mcpclient.NewStreamableHttpClient(httpSrv.URL)
	if err != nil {
		t.Fatalf("NewStreamableHttpClient() error = %v", err)
	}
	t.Cleanup(func() { c.Close() })

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := c.Initialize(context.Background(), mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "xmail-test", Version: "0.0.0"},
	}}); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	return c
}

// TestTransport_SendEmailBindsArguments drives a tool call through the
// real Streamable HTTP transport instead of calling the handler
// directly. Direct handler tests skip mcp.NewTypedToolHandler's
// BindArguments path — the exact code that once dropped a tool argument
// without any test noticing (see ARCHITECTURE.md §7), so this asserts
// arguments survive the JSON-RPC round trip.
func TestTransport_SendEmailBindsArguments(t *testing.T) {
	ctx := context.Background()
	s, svc := newTestServer(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var sent mailer.OutgoingMessage
	registerSender(svc, func(cfg account.ConnectionConfig, fromAddress, username, secret string) mailer.Sender {
		return &mockSender{sent: &sent}
	})

	c := dialMCP(t, s)
	res, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name: toolSendEmail,
		Arguments: map[string]any{
			"account_id": created.ID,
			"to":         []string{"rcpt@example.com"},
			"subject":    "Hello over the wire",
			"body_text":  "hi",
		},
	}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool() IsError = true, content = %+v", res.Content)
	}

	if sent.Subject != "Hello over the wire" {
		t.Errorf("bound Subject = %q, want %q", sent.Subject, "Hello over the wire")
	}
	if len(sent.To) != 1 || sent.To[0] != "rcpt@example.com" {
		t.Errorf("bound To = %v, want [rcpt@example.com]", sent.To)
	}
	if sent.BodyText != "hi" {
		t.Errorf("bound BodyText = %q, want %q", sent.BodyText, "hi")
	}
}

// TestTransport_CheckNewEmailsWireKeys decodes check_new_emails' response
// off the wire and asserts the JSON keys are unread_count/new_count —
// the same keys REST's /check emits from the shared account.CheckResult
// (PLAN.md §10.9 candidate D). Asserting the decoded struct's fields
// would not catch a bad `json` tag; this does.
func TestTransport_CheckNewEmailsWireKeys(t *testing.T) {
	ctx := context.Background()
	s, svc := newTestServer(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	registerIMAPFetcher(svc, func(cfg account.ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{unread: 4, fetchResult: []mailer.Message{{UID: "u1", Folder: "INBOX"}}}
	})

	c := dialMCP(t, s)
	res, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      toolCheckNewEmails,
		Arguments: map[string]any{"account_id": created.ID},
	}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool() IsError = true, content = %+v", res.Content)
	}

	// StructuredContent came back over the wire, so it is a decoded map
	// keyed by the actual JSON field names.
	sc, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want map[string]any", res.StructuredContent)
	}
	if sc["unread_count"] != float64(4) {
		t.Errorf("wire unread_count = %v, want 4", sc["unread_count"])
	}
	if sc["new_count"] != float64(1) {
		t.Errorf("wire new_count = %v, want 1 (first check, nothing cached yet)", sc["new_count"])
	}
}

// TestTransport_FetchEmailsEmptyIsArray: an empty mailbox must reach the
// client as an empty JSON array, not null — the same contract REST's
// /messages has (see the api package's sibling test).
func TestTransport_FetchEmailsEmptyIsArray(t *testing.T) {
	ctx := context.Background()
	s, svc := newTestServer(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	registerIMAPFetcher(svc, func(cfg account.ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcherChecker{} // Fetch returns nil
	})

	c := dialMCP(t, s)
	res, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      toolFetchEmails,
		Arguments: map[string]any{"account_id": created.ID},
	}})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool() IsError = true, content = %+v", res.Content)
	}
	sc, ok := res.StructuredContent.([]any)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want []any (an empty mailbox must serialize as [])", res.StructuredContent)
	}
	if len(sc) != 0 {
		t.Errorf("StructuredContent len = %d, want 0", len(sc))
	}
}
