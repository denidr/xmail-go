package account

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

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

// mockTester is a test double for account.ConnTester, standing in for a
// real mailer.Sender/Fetcher's TestConnection method without any
// network I/O — this is the point of the ConnTester interface (see
// PLAN.md §6.1: account.Service unit tests mock mailer entirely).
type mockTester struct{ err error }

func (m mockTester) TestConnection(ctx context.Context) error { return m.err }

func TestService_TestConnection(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t)

	created, err := svc.Create(ctx, sampleAccount(), "s3cret")
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	t.Run("no tester wired", func(t *testing.T) {
		if err := svc.TestConnection(ctx, created.ID, "smtp"); err == nil {
			t.Error("TestConnection() error = nil, want error (tester not wired)")
		}
	})

	t.Run("tester succeeds", func(t *testing.T) {
		svc.SetSMTPTester(func(cfg ConnectionConfig, username, secret string) ConnTester {
			if secret != "s3cret" {
				t.Errorf("factory got secret = %q, want %q", secret, "s3cret")
			}
			return mockTester{}
		})
		if err := svc.TestConnection(ctx, created.ID, "smtp"); err != nil {
			t.Errorf("TestConnection() error = %v, want nil", err)
		}
	})

	t.Run("tester fails", func(t *testing.T) {
		wantErr := errors.New("dial failed")
		svc.SetSMTPTester(func(cfg ConnectionConfig, username, secret string) ConnTester {
			return mockTester{err: wantErr}
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
