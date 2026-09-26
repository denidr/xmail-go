package smtp

import (
	"context"
	"testing"

	"xmail/internal/account"
	"xmail/internal/mailer"
)

func TestBuildMailClient_TLSModeMapping(t *testing.T) {
	cases := []struct {
		mode    account.TLSMode
		wantErr bool
	}{
		{account.TLSModeTLS, false},
		{account.TLSModeStartTLS, false},
		{account.TLSModeNone, false},
		{account.TLSMode("bogus"), true},
	}
	for _, tc := range cases {
		t.Run(string(tc.mode), func(t *testing.T) {
			c := New(account.ConnectionConfig{Host: "smtp.example.com", Port: 587, TLSMode: tc.mode}, "from@example.com", "user", "pass")
			_, err := c.buildMailClient()
			if tc.wantErr && err == nil {
				t.Error("buildMailClient() error = nil, want error for unknown tls_mode")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("buildMailClient() error = %v, want nil", err)
			}
		})
	}
}

func TestSend_RequiresFromAddress(t *testing.T) {
	c := New(account.ConnectionConfig{Host: "smtp.example.com", Port: 587, TLSMode: account.TLSModeNone}, "", "user", "pass")
	err := c.Send(context.Background(), mailer.OutgoingMessage{To: []string{"a@b.com"}, Subject: "hi"})
	if err == nil {
		t.Error("Send() with empty fromAddress error = nil, want error")
	}
}
