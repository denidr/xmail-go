//go:build integration

package smtp

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gosasl "github.com/emersion/go-sasl"
	gosmtp "github.com/emersion/go-smtp"

	"xmail/internal/account"
	"xmail/internal/mailer"
)

// capturedMessage is what the fake backend records for the test to
// assert against.
type capturedMessage struct {
	from string
	to   []string
	data string
}

type testBackend struct {
	mu   sync.Mutex
	msgs []capturedMessage

	// authUser/authPass, if authUser is non-empty, require CRAM-MD5 auth
	// with these credentials before Mail/Rcpt/Data are reachable — used
	// by TestIntegration_Send_RequiresAuth to prove internal/mailer/smtp
	// actually performs SMTP AUTH (see PLAN.md Fase 8 "crosscheck": a
	// prior version silently never sent AUTH at all despite credentials
	// being configured).
	authUser, authPass string
}

func (b *testBackend) NewSession(c *gosmtp.Conn) (gosmtp.Session, error) {
	return &testSession{backend: b}, nil
}

type testSession struct {
	backend *testBackend
	from    string
	to      []string
}

func (s *testSession) Reset()        {}
func (s *testSession) Logout() error { return nil }

var _ gosmtp.AuthSession = (*testSession)(nil)

func (s *testSession) AuthMechanisms() []string {
	if s.backend.authUser == "" {
		return nil
	}
	return []string{"CRAM-MD5"}
}

func (s *testSession) Auth(mech string) (gosasl.Server, error) {
	if mech != "CRAM-MD5" {
		return nil, fmt.Errorf("unsupported mechanism %q", mech)
	}
	return &cramMD5Server{username: s.backend.authUser, password: s.backend.authPass}, nil
}

// cramMD5Server implements gosasl.Server for RFC 2195 CRAM-MD5, matching
// what Go's stdlib net/smtp.CRAMMD5Auth (used internally by go-mail's
// SMTPAuthAutoDiscover) speaks on the wire. go-sasl only ships a
// client-side CRAM-MD5 implementation, so the server side is hand-rolled
// here, test-only.
type cramMD5Server struct {
	username, password string
	challenge          []byte
}

func (s *cramMD5Server) Next(response []byte) (challenge []byte, done bool, err error) {
	if response == nil {
		s.challenge = []byte(fmt.Sprintf("<%d.test@localhost>", time.Now().UnixNano()))
		return s.challenge, false, nil
	}
	parts := strings.SplitN(string(response), " ", 2)
	if len(parts) != 2 {
		return nil, false, errors.New("cram-md5: malformed response")
	}
	user, digestHex := parts[0], parts[1]
	if user != s.username {
		return nil, false, errors.New("cram-md5: unknown user")
	}
	mac := hmac.New(md5.New, []byte(s.password))
	mac.Write(s.challenge)
	want := hex.EncodeToString(mac.Sum(nil))
	if digestHex != want {
		return nil, false, errors.New("cram-md5: invalid credentials")
	}
	return nil, true, nil
}

func (s *testSession) Mail(from string, opts *gosmtp.MailOptions) error {
	s.from = from
	return nil
}

func (s *testSession) Rcpt(to string, opts *gosmtp.RcptOptions) error {
	s.to = append(s.to, to)
	return nil
}

func (s *testSession) Data(r io.Reader) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.backend.mu.Lock()
	s.backend.msgs = append(s.backend.msgs, capturedMessage{from: s.from, to: s.to, data: string(b)})
	s.backend.mu.Unlock()
	return nil
}

// startTestServer starts an in-process plaintext SMTP server (see
// PLAN.md §6.2: SMTP integration test) and returns its address and a
// shutdown func. AllowInsecureAuth is enabled since this is a local,
// non-TLS test fixture only.
func startTestServer(t *testing.T) (addr string, backend *testBackend) {
	t.Helper()
	return startTestServerWithAuth(t, "", "")
}

// startTestServerWithAuth is like startTestServer, but if authUser is
// non-empty, the server advertises CRAM-MD5 AUTH and requires it (see
// testSession.AuthMechanisms/Auth) before accepting MAIL/RCPT/DATA.
func startTestServerWithAuth(t *testing.T, authUser, authPass string) (addr string, backend *testBackend) {
	t.Helper()
	backend = &testBackend{authUser: authUser, authPass: authPass}
	server := gosmtp.NewServer(backend)
	server.Addr = "127.0.0.1:0"
	server.Domain = "localhost"
	server.AllowInsecureAuth = true
	server.ReadTimeout = 5 * time.Second
	server.WriteTimeout = 5 * time.Second

	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	go func() {
		_ = server.Serve(ln)
	}()
	t.Cleanup(func() { server.Close() })

	return ln.Addr().String(), backend
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return host, port
}

func TestIntegration_SendEmail_Plaintext(t *testing.T) {
	addr, backend := startTestServer(t)
	host, port := splitHostPort(t, addr)

	client := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, "from@example.com", "", "")

	msg := mailer.OutgoingMessage{
		To:       []string{"rcpt@example.com"},
		Subject:  "Integration Test",
		BodyText: "hello from xmail integration test",
	}
	if err := client.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.msgs) != 1 {
		t.Fatalf("backend received %d messages, want 1", len(backend.msgs))
	}
	got := backend.msgs[0]
	if got.from != "from@example.com" {
		t.Errorf("from = %q, want %q", got.from, "from@example.com")
	}
	if len(got.to) != 1 || got.to[0] != "rcpt@example.com" {
		t.Errorf("to = %v, want [rcpt@example.com]", got.to)
	}
	if !strings.Contains(got.data, "Integration Test") {
		t.Error("message data missing subject")
	}
	if !strings.Contains(got.data, "hello from xmail integration test") {
		t.Error("message data missing body")
	}
}

// TestIntegration_Send_CustomHeaders proves PRD.MD §6.2 "custom headers
// dasar" actually reaches the wire, not just that OutgoingMessage.Headers
// compiles.
func TestIntegration_Send_CustomHeaders(t *testing.T) {
	addr, backend := startTestServer(t)
	host, port := splitHostPort(t, addr)

	client := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, "from@example.com", "", "")

	msg := mailer.OutgoingMessage{
		To:       []string{"rcpt@example.com"},
		Subject:  "Custom Header Test",
		BodyText: "body",
		Headers:  map[string]string{"X-Priority": "1", "X-Xmail-Test": "yes"},
	}
	if err := client.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if len(backend.msgs) != 1 {
		t.Fatalf("backend received %d messages, want 1", len(backend.msgs))
	}
	data := backend.msgs[0].data
	if !strings.Contains(data, "X-Priority: 1") {
		t.Errorf("message data missing custom header X-Priority, got:\n%s", data)
	}
	if !strings.Contains(data, "X-Xmail-Test: yes") {
		t.Errorf("message data missing custom header X-Xmail-Test, got:\n%s", data)
	}
}

func TestIntegration_TestConnection_Plaintext(t *testing.T) {
	addr, _ := startTestServer(t)
	host, port := splitHostPort(t, addr)

	client := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, "", "", "")
	if err := client.TestConnection(context.Background()); err != nil {
		t.Errorf("TestConnection() error = %v", err)
	}
}

// TestIntegration_Send_ActuallyAuthenticates is a regression test: an
// earlier version of buildMailClient set WithUsername/WithPassword but
// never WithSMTPAuth, so go-mail's default SMTPAuthNoAuth meant AUTH was
// never sent at all — TestConnection/Send silently "succeeded" against
// servers requiring auth, even with a wrong password (caught by manually
// probing a real Gmail account during Fase 8 crosscheck; see PLAN.md).
// This locks that fix in via CI-runnable in-process server, using
// CRAM-MD5 since that's the only mechanism go-mail's AutoDiscover will
// pick over a non-TLS connection (see internal/mailer/smtp/client.go).
func TestIntegration_Send_ActuallyAuthenticates(t *testing.T) {
	t.Run("correct credentials succeed", func(t *testing.T) {
		addr, backend := startTestServerWithAuth(t, "testuser", "testpass")
		host, port := splitHostPort(t, addr)

		client := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, "from@example.com", "testuser", "testpass")
		if err := client.Send(context.Background(), mailer.OutgoingMessage{To: []string{"rcpt@example.com"}, Subject: "hi", BodyText: "body"}); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		backend.mu.Lock()
		defer backend.mu.Unlock()
		if len(backend.msgs) != 1 {
			t.Fatalf("backend received %d messages, want 1", len(backend.msgs))
		}
	})

	t.Run("wrong password is rejected", func(t *testing.T) {
		addr, backend := startTestServerWithAuth(t, "testuser", "testpass")
		host, port := splitHostPort(t, addr)

		client := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, "from@example.com", "testuser", "wrong-password")
		err := client.Send(context.Background(), mailer.OutgoingMessage{To: []string{"rcpt@example.com"}, Subject: "hi", BodyText: "body"})
		if err == nil {
			t.Fatal("Send() with wrong password error = nil, want an AUTH error")
		}
		backend.mu.Lock()
		defer backend.mu.Unlock()
		if len(backend.msgs) != 0 {
			t.Errorf("backend received %d messages despite failed auth, want 0", len(backend.msgs))
		}
	})

	t.Run("TestConnection also enforces auth", func(t *testing.T) {
		addr, _ := startTestServerWithAuth(t, "testuser", "testpass")
		host, port := splitHostPort(t, addr)

		client := New(account.ConnectionConfig{Host: host, Port: port, TLSMode: account.TLSModeNone}, "", "testuser", "wrong-password")
		if err := client.TestConnection(context.Background()); err == nil {
			t.Fatal("TestConnection() with wrong password error = nil, want an AUTH error")
		}
	})
}
