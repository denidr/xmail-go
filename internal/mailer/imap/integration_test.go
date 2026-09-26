//go:build integration

package imap

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"

	"xmail/internal/account"
	"xmail/internal/mailer"
)

// fakeMessage is one fixed message served by the fake IMAP backend.
type fakeMessage struct {
	seq        uint32
	uid        imapv2.UID
	subject    string
	from       string
	seen       bool
	attachment string // filename, or "" for no attachment
}

// fakeInboxTemplate is the immutable blueprint for a fresh mailbox —
// each startTestServer call clones it into a *fakeState so that tests
// mutating read/unread status (Store, see TestIntegration_MarkRead)
// never leak state into other tests sharing this package's test
// binary.
var fakeInboxTemplate = []fakeMessage{
	{seq: 1, uid: 101, subject: "Oldest", from: "a@example.com", seen: true, attachment: "invoice.pdf"},
	{seq: 2, uid: 102, subject: "Middle", from: "b@example.com", seen: false},
	{seq: 3, uid: 103, subject: "Newest", from: "c@example.com", seen: false},
}

const (
	testUsername = "testuser"
	testPassword = "testpass"
	testUnseen   = uint32(2)
)

// fakeState is the mutable, per-test-server mailbox state shared by
// all fakeSession values created for one startTestServer call.
type fakeState struct {
	mu       sync.Mutex
	messages []fakeMessage
}

// fakeSession implements imapserver.Session with just enough behavior
// to exercise Client.Fetch/Check/TestConnection/MarkRead (see PLAN.md
// §6.2: IMAP integration test uses an in-process imapserver, not a
// real mailbox).
type fakeSession struct {
	state *fakeState
}

func (s *fakeSession) Close() error { return nil }

func (s *fakeSession) Login(username, password string) error {
	if username != testUsername || password != testPassword {
		return errors.New("invalid credentials")
	}
	return nil
}

func (s *fakeSession) Select(mailbox string, options *imapv2.SelectOptions) (*imapv2.SelectData, error) {
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	return &imapv2.SelectData{NumMessages: uint32(len(s.state.messages))}, nil
}

func (s *fakeSession) Create(mailbox string, options *imapv2.CreateOptions) error { return nil }
func (s *fakeSession) Delete(mailbox string) error                                { return nil }
func (s *fakeSession) Rename(mailbox, newName string, options *imapv2.RenameOptions) error {
	return nil
}
func (s *fakeSession) Subscribe(mailbox string) error   { return nil }
func (s *fakeSession) Unsubscribe(mailbox string) error { return nil }
func (s *fakeSession) List(w *imapserver.ListWriter, ref string, patterns []string, options *imapv2.ListOptions) error {
	return nil
}

func (s *fakeSession) Status(mailbox string, options *imapv2.StatusOptions) (*imapv2.StatusData, error) {
	data := &imapv2.StatusData{Mailbox: mailbox}
	if options.NumUnseen {
		n := testUnseen
		data.NumUnseen = &n
	}
	return data, nil
}

func (s *fakeSession) Append(mailbox string, r imapv2.LiteralReader, options *imapv2.AppendOptions) (*imapv2.AppendData, error) {
	return nil, errors.New("append not supported by fake server")
}
func (s *fakeSession) Poll(w *imapserver.UpdateWriter, allowExpunge bool) error { return nil }
func (s *fakeSession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	return nil
}
func (s *fakeSession) Unselect() error { return nil }
func (s *fakeSession) Expunge(w *imapserver.ExpungeWriter, uids *imapv2.UIDSet) error {
	return nil
}
func (s *fakeSession) Search(kind imapserver.NumKind, criteria *imapv2.SearchCriteria, options *imapv2.SearchOptions) (*imapv2.SearchData, error) {
	return nil, errors.New("search not supported by fake server")
}

// bodyStructureFor returns a minimal but valid BODYSTRUCTURE: a lone
// text/plain part, or — for messages with a non-empty m.attachment —
// a multipart/mixed with a second application/octet-stream part
// carrying that filename via Content-Disposition, matching what
// attachmentNames (client.go) looks for.
func bodyStructureFor(m fakeMessage) imapv2.BodyStructure {
	// Extended must be non-nil on every part (even with a zero value)
	// once the client requests BODYSTRUCTURE — imapserver panics
	// ("client requested extended body structure but a non-extended
	// one is written back") if any part in the tree looks non-extended.
	text := &imapv2.BodyStructureSinglePart{
		Type: "text", Subtype: "plain",
		Encoding: "7bit", Size: 100,
		Text:     &imapv2.BodyStructureText{NumLines: 5},
		Extended: &imapv2.BodyStructureSinglePartExt{},
	}
	if m.attachment == "" {
		return text
	}
	attachment := &imapv2.BodyStructureSinglePart{
		Type:     "application",
		Subtype:  "octet-stream",
		Encoding: "base64",
		Size:     200,
		Extended: &imapv2.BodyStructureSinglePartExt{
			Disposition: &imapv2.BodyStructureDisposition{
				Value:  "attachment",
				Params: map[string]string{"filename": m.attachment},
			},
		},
	}
	return &imapv2.BodyStructureMultiPart{
		Subtype:  "mixed",
		Children: []imapv2.BodyStructure{text, attachment},
		Extended: &imapv2.BodyStructureMultiPartExt{},
	}
}

func (s *fakeSession) Fetch(w *imapserver.FetchWriter, numSet imapv2.NumSet, options *imapv2.FetchOptions) error {
	seqSet, ok := numSet.(imapv2.SeqSet)
	if !ok {
		return errors.New("fake server only supports SeqSet fetch")
	}
	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	for _, m := range s.state.messages {
		if !seqSet.Contains(m.seq) {
			continue
		}
		rw := w.CreateMessage(m.seq)
		if options.UID {
			rw.WriteUID(m.uid)
		}
		if options.Envelope {
			rw.WriteEnvelope(&imapv2.Envelope{
				Subject: m.subject,
				From:    []imapv2.Address{{Mailbox: m.from, Host: ""}},
				Date:    time.Unix(0, 0),
			})
		}
		if options.Flags {
			var flags []imapv2.Flag
			if m.seen {
				flags = append(flags, imapv2.FlagSeen)
			}
			rw.WriteFlags(flags)
		}
		if options.BodyStructure != nil {
			rw.WriteBodyStructure(bodyStructureFor(m))
		}
		if err := rw.Close(); err != nil {
			return err
		}
	}
	return nil
}

// Store implements enough of STORE +FLAGS (\Seen) for
// TestIntegration_MarkRead — it looks up the message by UID (the only
// numSet kind Client.MarkRead sends) and, for a StoreFlagsAdd of
// \Seen, marks it read in the shared fakeState.
func (s *fakeSession) Store(w *imapserver.FetchWriter, numSet imapv2.NumSet, flags *imapv2.StoreFlags, options *imapv2.StoreOptions) error {
	uidSet, ok := numSet.(imapv2.UIDSet)
	if !ok {
		return errors.New("fake server only supports UIDSet store")
	}
	if flags.Op != imapv2.StoreFlagsAdd {
		return errors.New("fake server only supports StoreFlagsAdd")
	}
	addsSeen := false
	for _, f := range flags.Flags {
		if f == imapv2.FlagSeen {
			addsSeen = true
		}
	}
	if !addsSeen {
		return nil
	}

	s.state.mu.Lock()
	defer s.state.mu.Unlock()
	for i, m := range s.state.messages {
		if uidSet.Contains(m.uid) {
			s.state.messages[i].seen = true
		}
	}
	return nil
}

func (s *fakeSession) Copy(numSet imapv2.NumSet, dest string) (*imapv2.CopyData, error) {
	return nil, errors.New("copy not supported by fake server")
}

func startTestServer(t *testing.T) (host string, port int) {
	t.Helper()
	messages := make([]fakeMessage, len(fakeInboxTemplate))
	copy(messages, fakeInboxTemplate)
	state := &fakeState{messages: messages}

	opts := &imapserver.Options{
		NewSession: func(c *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &fakeSession{state: state}, nil, nil
		},
		InsecureAuth: true,
	}
	server := imapserver.New(opts)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() { server.Close() })

	h, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port = 0
	for _, r := range p {
		port = port*10 + int(r-'0')
	}
	return h, port
}

func TestIntegration_TestConnection(t *testing.T) {
	host, port := startTestServer(t)
	c := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, testUsername, testPassword)
	if err := c.TestConnection(context.Background()); err != nil {
		t.Fatalf("TestConnection() error = %v", err)
	}
}

func TestIntegration_TestConnection_WrongPassword(t *testing.T) {
	host, port := startTestServer(t)
	c := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, testUsername, "wrong")
	if err := c.TestConnection(context.Background()); err == nil {
		t.Fatal("TestConnection() error = nil, want error for wrong password")
	}
}

func TestIntegration_Fetch(t *testing.T) {
	host, port := startTestServer(t)
	c := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, testUsername, testPassword)

	msgs, err := c.Fetch(context.Background(), "INBOX", 2, 0)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("Fetch() returned %d messages, want 2", len(msgs))
	}
	// Newest-first: seq 3 ("Newest") then seq 2 ("Middle").
	if msgs[0].Subject != "Newest" || msgs[1].Subject != "Middle" {
		t.Errorf("Fetch() order = [%q, %q], want [Newest, Middle]", msgs[0].Subject, msgs[1].Subject)
	}
	if msgs[0].Folder != "INBOX" || msgs[1].Folder != "INBOX" {
		t.Errorf("Fetch() folders = [%q, %q], want the fetched folder %q on each", msgs[0].Folder, msgs[1].Folder, "INBOX")
	}
	if msgs[0].IsRead {
		t.Error("msgs[0] (seq 3, unseen) reported as read")
	}
}

// TestIntegration_Fetch_ZeroLimit is a regression test: imapv2.SeqSet.AddRange
// silently swaps an inverted (start > stop) range instead of treating it as
// empty, so a naive "start := end - limit + 1" computation with limit == 0
// used to fetch one unintended message instead of zero.
func TestIntegration_Fetch_ZeroLimit(t *testing.T) {
	host, port := startTestServer(t)
	c := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, testUsername, testPassword)

	msgs, err := c.Fetch(context.Background(), "INBOX", 0, 0)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("Fetch(limit=0) returned %d messages, want 0", len(msgs))
	}
}

// TestIntegration_Fetch_Attachments proves the attachment filename
// list (PRD.MD §6.3) actually comes back from a real BODYSTRUCTURE
// fetch/parse round trip, not just that mailer.Message has the field.
func TestIntegration_Fetch_Attachments(t *testing.T) {
	host, port := startTestServer(t)
	c := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, testUsername, testPassword)

	msgs, err := c.Fetch(context.Background(), "INBOX", 10, 0)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	var oldest *mailer.Message
	for i := range msgs {
		if msgs[i].Subject == "Oldest" {
			oldest = &msgs[i]
		}
	}
	if oldest == nil {
		t.Fatal("fixture message \"Oldest\" not found in Fetch() result")
	}
	if len(oldest.Attachments) != 1 || oldest.Attachments[0] != "invoice.pdf" {
		t.Errorf("Oldest.Attachments = %v, want [invoice.pdf]", oldest.Attachments)
	}

	for _, m := range msgs {
		if m.Subject != "Oldest" && len(m.Attachments) != 0 {
			t.Errorf("%s.Attachments = %v, want none", m.Subject, m.Attachments)
		}
	}
}

// TestIntegration_MarkRead proves MarkRead actually sends STORE +FLAGS
// \Seen and that a subsequent Fetch reflects it — see PRD.MD §6.3
// "mark as read" and CODE_REVIEW.md "requirement hilang".
func TestIntegration_MarkRead(t *testing.T) {
	host, port := startTestServer(t)
	c := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, testUsername, testPassword)

	// seq 2 ("Middle", uid 102) starts unseen in fakeInboxTemplate.
	if err := c.MarkRead(context.Background(), "INBOX", "102"); err != nil {
		t.Fatalf("MarkRead() error = %v", err)
	}

	msgs, err := c.Fetch(context.Background(), "INBOX", 10, 0)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	var middle *mailer.Message
	for i := range msgs {
		if msgs[i].UID == "102" {
			middle = &msgs[i]
		}
	}
	if middle == nil {
		t.Fatal("fixture message uid=102 not found in Fetch() result")
	}
	if !middle.IsRead {
		t.Error("message uid=102 IsRead = false after MarkRead(), want true")
	}
}

func TestIntegration_Check(t *testing.T) {
	host, port := startTestServer(t)
	c := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, testUsername, testPassword)

	unread, err := c.Check(context.Background(), "INBOX")
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if unread != int(testUnseen) {
		t.Errorf("Check() unread = %d, want %d", unread, testUnseen)
	}
}
