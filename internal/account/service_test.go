package account

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"xmail/internal/mailer"
	"xmail/internal/storage"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := storage.Open(filepath.Join(t.TempDir(), "xmail.db"))
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	repo := NewRepository(db, bytes.Repeat([]byte{0x22}, 32))
	return NewService(repo)
}

// registerSMTP/registerIMAP wire test doubles as SMTP/IMAP protocol
// implementations — the tests mock the registered Protocol, not a
// concrete mailer package (see PLAN.md §6.1).
func registerSMTP(svc *Service, fn func(cfg ConnectionConfig, fromAddress, username, secret string) mailer.Sender) {
	svc.RegisterProtocol(ProtocolSMTP, Protocol{Sender: fn})
}

func registerIMAP(svc *Service, fn func(cfg ConnectionConfig, username, secret string) mailer.Fetcher) {
	svc.RegisterProtocol(ProtocolIMAP, Protocol{Fetcher: fn})
}

func registerPOP3(svc *Service, fn func(cfg ConnectionConfig, username, secret string) mailer.Fetcher) {
	svc.RegisterProtocol(ProtocolPOP3, Protocol{Fetcher: fn})
}

func TestValidate(t *testing.T) {
	valid := Account{
		Name: "n", Email: "e@x.com", Username: "u",
		SMTP: &ConnectionConfig{Host: "h", Port: 587, TLSMode: TLSModeStartTLS},
	}

	cases := []struct {
		name    string
		mutate  func(a Account) Account
		secret  string
		wantErr bool
	}{
		{"valid", func(a Account) Account { return a }, "s", false},
		{"missing name", func(a Account) Account { a.Name = ""; return a }, "s", true},
		{"missing email", func(a Account) Account { a.Email = ""; return a }, "s", true},
		{"missing username", func(a Account) Account { a.Username = ""; return a }, "s", true},
		{"missing secret", func(a Account) Account { return a }, "", true},
		{"no protocol configured", func(a Account) Account { a.SMTP = nil; return a }, "s", true},
		{"bad port", func(a Account) Account {
			a.SMTP = &ConnectionConfig{Host: "h", Port: 0, TLSMode: TLSModeTLS}
			return a
		}, "s", true},
		{"bad tls mode", func(a Account) Account {
			a.SMTP = &ConnectionConfig{Host: "h", Port: 587, TLSMode: "bogus"}
			return a
		}, "s", true},
		{"pop3 starttls rejected (unsupported by mailer/pop3)", func(a Account) Account {
			a.POP3 = &ConnectionConfig{Host: "h", Port: 110, TLSMode: TLSModeStartTLS}
			return a
		}, "s", true},
		{"pop3 tls accepted", func(a Account) Account {
			a.POP3 = &ConnectionConfig{Host: "h", Port: 995, TLSMode: TLSModeTLS}
			return a
		}, "s", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.mutate(valid), true, tc.secret)
			if tc.wantErr && err == nil {
				t.Error("Validate() error = nil, want error")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Validate() error = %v, want nil", err)
			}
			if tc.wantErr && err != nil && !errors.Is(err, ErrValidation) {
				t.Errorf("Validate() error = %v, want wrapped ErrValidation", err)
			}
		})
	}
}

func TestService_Create_RejectsInvalid(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Create(context.Background(), Account{}, "")
	if !errors.Is(err, ErrValidation) {
		t.Errorf("Create() error = %v, want ErrValidation", err)
	}
}

// TestService_Update_SecretOptional locks in the fix for
// CODE_REVIEW.md "Mysterious Name / magic value": Update must accept
// a nil secret (password left unchanged) without needing any fake
// placeholder value internally, while an explicitly-provided empty
// string is still rejected as invalid.
func TestService_Update_SecretOptional(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	created.Name = "Renamed"
	if _, err := svc.Update(ctx, created, nil); err != nil {
		t.Errorf("Update() with nil secret error = %v, want nil", err)
	}

	empty := ""
	if _, err := svc.Update(ctx, created, &empty); !errors.Is(err, ErrValidation) {
		t.Errorf("Update() with explicit empty secret error = %v, want ErrValidation", err)
	}
}

// mockSender is a test double for mailer.Sender, standing in for a real
// smtp.Client without any network I/O — Service tests mock the
// registered Protocol's constructor entirely (see PLAN.md §6.1).
type mockSender struct {
	err error
}

func (m mockSender) Send(ctx context.Context, msg mailer.OutgoingMessage) error { return nil }
func (m mockSender) TestConnection(ctx context.Context) error                   { return m.err }

// mockFetcher is a test double for mailer.Fetcher (fetch-only
// protocols, i.e. imap/pop3's branch of TestConnection).
type mockFetcher struct{ err error }

func (m mockFetcher) Fetch(ctx context.Context, folder string, limit, offset int) ([]mailer.Message, error) {
	return nil, nil
}
func (m mockFetcher) TestConnection(ctx context.Context) error { return m.err }

func TestService_Send_UsesAccountEmailAsFrom(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var gotFrom string
	registerSMTP(svc, func(cfg ConnectionConfig, fromAddress, username, secret string) mailer.Sender {
		gotFrom = fromAddress
		return mockSender{}
	})
	if err := svc.Send(ctx, created.ID, mailer.OutgoingMessage{To: []string{"a@b.c"}}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotFrom != created.Email {
		t.Errorf("Send built a sender with fromAddress = %q, want the account email %q", gotFrom, created.Email)
	}
}

// TestService_POP3_HasNoCheckerOrMarker locks in that a folder-less, flag-less
// protocol degrades cleanly: no unread count, and mark-read is a validation
// error — rather than a panic or a silent no-op.
func TestService_POP3_HasNoCheckerOrMarker(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	acct := sampleAccount()
	acct.POP3 = &ConnectionConfig{Host: "pop.example.com", Port: 995, TLSMode: TLSModeTLS}
	created, err := svc.Create(ctx, acct, "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// mockFetcher implements Fetcher only (no Checker/Marker), like pop3.Client.
	registerPOP3(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
		return mockFetcher{}
	})

	unread, _, err := svc.CheckNew(ctx, created.ID, "pop3", "INBOX")
	if err != nil {
		t.Errorf("CheckNew(pop3) error = %v, want nil (merely no unread count)", err)
	}
	if unread != 0 {
		t.Errorf("CheckNew(pop3) unread = %d, want 0 (POP3 has no unseen-flag concept)", unread)
	}

	if err := svc.MarkRead(ctx, created.ID, "pop3", "INBOX", "1"); !errors.Is(err, ErrValidation) {
		t.Errorf("MarkRead(pop3) error = %v, want ErrValidation", err)
	}
}

func TestService_TestConnection(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Run("protocol not registered", func(t *testing.T) {
		if err := svc.TestConnection(ctx, created.ID, "smtp"); err == nil {
			t.Error("TestConnection() error = nil, want error (smtp not registered)")
		}
	})

	t.Run("succeeds", func(t *testing.T) {
		registerSMTP(svc, func(cfg ConnectionConfig, fromAddress, username, secret string) mailer.Sender {
			if fromAddress != "" {
				t.Errorf("TestConnection built a sender with fromAddress = %q, want \"\" (only Send needs the from address)", fromAddress)
			}
			if secret != "s3cret" {
				t.Errorf("constructor got secret = %q, want %q", secret, "s3cret")
			}
			return mockSender{}
		})
		if err := svc.TestConnection(ctx, created.ID, "smtp"); err != nil {
			t.Errorf("TestConnection() error = %v, want nil", err)
		}
	})

	// The Fetcher branch is what every IMAP/POP3 test-connection — and
	// every call with protocol omitted (default imap) — goes through.
	t.Run("fetcher branch, and default protocol", func(t *testing.T) {
		var gotUser, gotSecret string
		registerIMAP(svc, func(cfg ConnectionConfig, username, secret string) mailer.Fetcher {
			gotUser, gotSecret = username, secret
			return mockFetcher{}
		})
		if err := svc.TestConnection(ctx, created.ID, ""); err != nil {
			t.Errorf("TestConnection(\"\") error = %v, want nil (should default to imap)", err)
		}
		if err := svc.TestConnection(ctx, created.ID, "imap"); err != nil {
			t.Errorf("TestConnection(\"imap\") error = %v, want nil", err)
		}
		if gotUser != "test@example.com" || gotSecret != "s3cret" {
			t.Errorf("fetcher constructor got (%q, %q), want (test@example.com, s3cret)", gotUser, gotSecret)
		}
	})

	t.Run("fails", func(t *testing.T) {
		wantErr := errors.New("dial failed")
		registerSMTP(svc, func(cfg ConnectionConfig, fromAddress, username, secret string) mailer.Sender {
			return mockSender{err: wantErr}
		})
		if err := svc.TestConnection(ctx, created.ID, "smtp"); !errors.Is(err, wantErr) {
			t.Errorf("TestConnection() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("protocol not configured on account", func(t *testing.T) {
		if err := svc.TestConnection(ctx, created.ID, "pop3"); err == nil {
			t.Error("TestConnection() error = nil, want error (pop3 not configured)")
		}
	})

	t.Run("unknown protocol", func(t *testing.T) {
		if err := svc.TestConnection(ctx, created.ID, "ftp"); !errors.Is(err, ErrValidation) {
			t.Errorf("TestConnection() error = %v, want ErrValidation", err)
		}
	})
}
