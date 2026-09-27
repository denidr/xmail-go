// Package mcpserver exposes xmail's account/mailer capabilities as MCP
// tools (list_accounts, send_email, fetch_emails, check_new_emails,
// list_folders),
// so MCP clients (Claude Desktop, Claude Code, etc.) can use them
// directly. Delegates to the same account.Service used by internal/api
// — no duplicated business logic.
package mcpserver

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"xmail/internal/account"
	"xmail/internal/mailer"
)

const serverName = "xmail"

// Tool names, shared by registerTools and each handler's error prefix so
// the two lists can't drift apart.
const (
	toolListAccounts   = "list_accounts"
	toolSendEmail      = "send_email"
	toolFetchEmails    = "fetch_emails"
	toolCheckNewEmails = "check_new_emails"
	toolListFolders    = "list_folders"
)

// Server wraps the MCP server instance and its tool handlers.
type Server struct {
	mcp     *server.MCPServer
	service *account.Service
}

// New builds a Server with all 5 tools registered.
// version is reported to MCP clients during the initialize handshake —
// callers pass the same build-time-stamped version used everywhere
// else (see cmd/xmail's and cmd/xmail-tray's `version` var, set via
// -ldflags -X main.version=... by scripts/release.sh), not a separate
// hardcoded value, so an MCP client can't see a stale/wrong version
// number for a release build (previously this was a hardcoded "0.1.0"
// disconnected from the real build stamping — see CODE_REVIEW.md).
func New(service *account.Service, version string) *Server {
	if version == "" {
		version = "dev"
	}
	s := &Server{
		mcp:     server.NewMCPServer(serverName, version),
		service: service,
	}
	s.registerTools()
	return s
}

// HTTPHandler exposes the MCP server over the Streamable HTTP
// transport (https://modelcontextprotocol.io streamable-http), so it
// can be mounted onto xmail's existing HTTP server (see internal/api,
// internal/app) instead of requiring a separate process/port.
func (s *Server) HTTPHandler() http.Handler {
	return server.NewStreamableHTTPServer(s.mcp)
}

// ServeStdio runs the MCP server over stdio — useful when an MCP
// client (e.g. Claude Desktop) launches xmail directly as a
// subprocess rather than connecting over HTTP. Only started when
// XMAIL_MCP_STDIO is enabled (see internal/config), since it takes
// over the process's stdin/stdout.
func (s *Server) ServeStdio() error {
	return server.ServeStdio(s.mcp)
}

func (s *Server) registerTools() {
	s.mcp.AddTool(
		mcp.NewTool(toolListAccounts,
			mcp.WithDescription("List configured email accounts (id, name, email only — never credentials)."),
		),
		s.handleListAccounts,
	)

	s.mcp.AddTool(
		mcp.NewTool(toolSendEmail,
			mcp.WithDescription("Send an email from one of the configured accounts via SMTP."),
			mcp.WithString("account_id", mcp.Required(), mcp.Description("ID of the account to send from (see list_accounts).")),
			mcp.WithArray("to", mcp.Required(), mcp.Description("Recipient email addresses."), mcp.Items(map[string]any{"type": "string"})),
			mcp.WithArray("cc", mcp.Description("CC email addresses."), mcp.Items(map[string]any{"type": "string"})),
			mcp.WithArray("bcc", mcp.Description("BCC email addresses."), mcp.Items(map[string]any{"type": "string"})),
			mcp.WithString("subject", mcp.Required(), mcp.Description("Email subject.")),
			mcp.WithString("body_text", mcp.Description("Plain-text body.")),
			mcp.WithString("body_html", mcp.Description("HTML body (optional; sent as an alternative to body_text if both are set).")),
			mcp.WithObject("headers", mcp.Description("Optional custom header lines, e.g. {\"X-Priority\": \"1\", \"Reply-To\": \"other@example.com\"}."), mcp.AdditionalProperties(map[string]any{"type": "string"})),
		),
		mcp.NewTypedToolHandler(s.handleSendEmail),
	)

	s.mcp.AddTool(
		mcp.NewTool(toolFetchEmails,
			mcp.WithDescription("Fetch recent emails from an account's mailbox (IMAP or POP3)."),
			mcp.WithString("account_id", mcp.Required(), mcp.Description("ID of the account to fetch from (see list_accounts).")),
			mcp.WithString("protocol", mcp.Description("\"imap\" or \"pop3\". Defaults to \"imap\".")),
			mcp.WithString("folder", mcp.Description("Mailbox folder (IMAP only). Defaults to \"INBOX\".")),
			mcp.WithNumber("limit", mcp.Description("Max messages to return. Defaults to 20.")),
			mcp.WithBoolean("refresh", mcp.Description("Force a live fetch from the mail server instead of serving cached results. Defaults to false.")),
		),
		mcp.NewTypedToolHandler(s.handleFetchEmails),
	)

	s.mcp.AddTool(
		mcp.NewTool(toolCheckNewEmails,
			mcp.WithDescription("Check unread/new email counts for an account without downloading messages."),
			mcp.WithString("account_id", mcp.Required(), mcp.Description("ID of the account to check (see list_accounts).")),
			mcp.WithString("protocol", mcp.Description("\"imap\" or \"pop3\". Defaults to \"imap\".")),
			mcp.WithString("folder", mcp.Description("Mailbox folder (IMAP only). Defaults to \"INBOX\".")),
		),
		mcp.NewTypedToolHandler(s.handleCheckNewEmails),
	)

	s.mcp.AddTool(
		mcp.NewTool(toolListFolders,
			mcp.WithDescription("List the mailboxes (folders) available on an account's IMAP server, with their delimiter and attributes (e.g. \\Sent). Use this to discover exact folder names before fetching."),
			mcp.WithString("account_id", mcp.Required(), mcp.Description("ID of the account to list folders for (see list_accounts).")),
			mcp.WithString("protocol", mcp.Description("Only \"imap\" supports folders. Defaults to \"imap\".")),
		),
		mcp.NewTypedToolHandler(s.handleListFolders),
	)
}

// accountSummary is the shape returned by list_accounts — intentionally
// separate from internal/api's DTOs and from account.Account: never
// includes connection config or credentials.
type accountSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (s *Server) handleListAccounts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	accounts, err := s.service.List(ctx)
	if err != nil {
		return mcp.NewToolResultErrorFromErr(toolListAccounts+" failed", err), nil
	}
	out := make([]accountSummary, 0, len(accounts))
	for _, a := range accounts {
		out = append(out, accountSummary{ID: a.ID, Name: a.Name, Email: a.Email})
	}
	return mcp.NewToolResultStructuredOnly(out), nil
}

type sendEmailArgs struct {
	AccountID string            `json:"account_id"`
	To        []string          `json:"to"`
	CC        []string          `json:"cc,omitempty"`
	BCC       []string          `json:"bcc,omitempty"`
	Subject   string            `json:"subject"`
	BodyText  string            `json:"body_text,omitempty"`
	BodyHTML  string            `json:"body_html,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	// No attachment support here — unlike REST, kept that way
	// deliberately: base64-encoding binary attachments into MCP tool
	// call arguments is a poor fit for how MCP clients typically
	// construct tool calls, whereas REST's JSON body already has to
	// support it either way.
}

func (s *Server) handleSendEmail(ctx context.Context, req mcp.CallToolRequest, args sendEmailArgs) (*mcp.CallToolResult, error) {
	if args.AccountID == "" {
		return mcp.NewToolResultError("account_id is required"), nil
	}
	if len(args.To) == 0 {
		return mcp.NewToolResultError("to is required (at least one recipient)"), nil
	}
	msg := mailer.OutgoingMessage{
		To:       args.To,
		CC:       args.CC,
		BCC:      args.BCC,
		Subject:  args.Subject,
		BodyText: args.BodyText,
		BodyHTML: args.BodyHTML,
		Headers:  args.Headers,
	}
	if err := s.service.Send(ctx, args.AccountID, msg); err != nil {
		return mcp.NewToolResultErrorFromErr(toolSendEmail+" failed", err), nil
	}
	return mcp.NewToolResultStructuredOnly(map[string]string{"status": "sent"}), nil
}

type fetchEmailsArgs struct {
	AccountID string `json:"account_id"`
	Protocol  string `json:"protocol,omitempty"`
	Folder    string `json:"folder,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	// Refresh forces a live dial to the mail server instead of serving
	// from messages_cache — see account.Service.FetchMessages.
	Refresh bool `json:"refresh,omitempty"`
}

func (s *Server) handleFetchEmails(ctx context.Context, req mcp.CallToolRequest, args fetchEmailsArgs) (*mcp.CallToolResult, error) {
	if args.AccountID == "" {
		return mcp.NewToolResultError("account_id is required"), nil
	}
	// protocol/folder/limit passed through as-is: account.Service
	// defaults empty protocol/folder and limit <= 0 centrally, so REST
	// and MCP can't drift on what "unspecified" means (see
	// CODE_REVIEW.md "Duplicated Code").
	msgs, err := s.service.FetchMessages(ctx, args.AccountID, args.Protocol, args.Folder, args.Limit, 0, args.Refresh)
	if err != nil {
		return mcp.NewToolResultErrorFromErr(toolFetchEmails+" failed", err), nil
	}
	// An empty mailbox must reach the client as [], not null (same as REST).
	if msgs == nil {
		msgs = []mailer.Message{}
	}
	return mcp.NewToolResultStructuredOnly(msgs), nil
}

type checkNewEmailsArgs struct {
	AccountID string `json:"account_id"`
	Protocol  string `json:"protocol,omitempty"`
	Folder    string `json:"folder,omitempty"`
}

func (s *Server) handleCheckNewEmails(ctx context.Context, req mcp.CallToolRequest, args checkNewEmailsArgs) (*mcp.CallToolResult, error) {
	if args.AccountID == "" {
		return mcp.NewToolResultError("account_id is required"), nil
	}

	unread, newCount, err := s.service.CheckNew(ctx, args.AccountID, args.Protocol, args.Folder)
	if err != nil {
		return mcp.NewToolResultErrorFromErr(toolCheckNewEmails+" failed", err), nil
	}
	// The same account.CheckResult REST serializes (see internal/api) —
	// one shared shape for both skins.
	return mcp.NewToolResultStructuredOnly(account.CheckResult{Unread: unread, New: newCount}), nil
}

type listFoldersArgs struct {
	AccountID string `json:"account_id"`
	Protocol  string `json:"protocol,omitempty"`
}

func (s *Server) handleListFolders(ctx context.Context, req mcp.CallToolRequest, args listFoldersArgs) (*mcp.CallToolResult, error) {
	if args.AccountID == "" {
		return mcp.NewToolResultError("account_id is required"), nil
	}

	folders, err := s.service.ListFolders(ctx, args.AccountID, args.Protocol)
	if err != nil {
		return mcp.NewToolResultErrorFromErr(toolListFolders+" failed", err), nil
	}
	// An empty folder list must reach the client as [], not null (same as REST).
	if folders == nil {
		folders = []mailer.Folder{}
	}
	return mcp.NewToolResultStructuredOnly(folders), nil
}
