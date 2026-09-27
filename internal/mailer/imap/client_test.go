package imap

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	imapv2 "github.com/emersion/go-imap/v2"

	"xmail/internal/mailer"
)

// TestClassifyFolderErr_NonExistent locks in that a "no such mailbox"
// status response becomes mailer.ErrFolderNotFound (so account.Service
// can answer 400 instead of 500), while keeping the server's own text —
// see PLAN-FOLDERS.md §3.3/§3.8.
func TestClassifyFolderErr_NonExistent(t *testing.T) {
	in := &imapv2.Error{
		Code: imapv2.ResponseCodeNonExistent,
		Text: "Unknown Mailbox: [Gmail]/Sent Mail (Failure)",
	}
	err := classifyFolderErr(in)
	if !errors.Is(err, mailer.ErrFolderNotFound) {
		t.Fatalf("classifyFolderErr() = %v, want wrapped mailer.ErrFolderNotFound", err)
	}
	// The server's explanation must survive so the caller can show it.
	if !strings.Contains(err.Error(), "Unknown Mailbox") {
		t.Errorf("classifyFolderErr() = %q, lost the server text", err)
	}
}

// TestClassifyFolderErr_TryCreate: TRYCREATE is the other response code
// (RFC 9051) a server uses to say the mailbox isn't there.
func TestClassifyFolderErr_TryCreate(t *testing.T) {
	err := classifyFolderErr(&imapv2.Error{Code: imapv2.ResponseCodeTryCreate, Text: "try create"})
	if !errors.Is(err, mailer.ErrFolderNotFound) {
		t.Errorf("classifyFolderErr(TRYCREATE) = %v, want mailer.ErrFolderNotFound", err)
	}
}

// TestClassifyFolderErr_OtherCodeUnchanged is the guard against
// over-classifying: a real server problem (e.g. INUSE) must stay a
// server error, not be misreported as a bad folder name.
func TestClassifyFolderErr_OtherCodeUnchanged(t *testing.T) {
	in := &imapv2.Error{Code: imapv2.ResponseCodeInUse, Text: "mailbox in use"}
	if got := classifyFolderErr(in); got != in {
		t.Errorf("classifyFolderErr() = %v, want the original error unchanged", got)
	}
}

// TestClassifyFolderErr_NonImapErrorUnchanged: a plain network/dial
// error has no response code at all and must pass through untouched.
func TestClassifyFolderErr_NonImapErrorUnchanged(t *testing.T) {
	in := errors.New("dial tcp: connection refused")
	if got := classifyFolderErr(in); got != in {
		t.Errorf("classifyFolderErr() = %v, want the original error unchanged", got)
	}
}

// TestClassifyFolderErr_Wrapped: the sentinel must survive when the
// IMAP error arrives wrapped (as it does from Select/Status .Wait()).
func TestClassifyFolderErr_Wrapped(t *testing.T) {
	in := fmt.Errorf("imap: select: %w", &imapv2.Error{Code: imapv2.ResponseCodeNonExistent, Text: "nope"})
	if err := classifyFolderErr(in); !errors.Is(err, mailer.ErrFolderNotFound) {
		t.Errorf("classifyFolderErr(wrapped) = %v, want mailer.ErrFolderNotFound", err)
	}
}
