//go:build windows && xmailtray

package main

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xmail/internal/winservice"
)

// TestSetupLoggingIn_WritesToFile pins the fix for the release .exe
// vanishing silently: a windowsgui process has no console, so the log
// file is the only trail a user can inspect afterwards.
func TestSetupLoggingIn_WritesToFile(t *testing.T) {
	prev := log.Writer()
	t.Cleanup(func() { log.SetOutput(prev) })

	base := t.TempDir()
	path, closeLog := setupLoggingIn(base)
	if path == "" {
		t.Fatal("setupLoggingIn returned no path for a writable base dir")
	}
	want := filepath.Join(base, "xmail", logFileName)
	if path != want {
		t.Errorf("log path = %q, want %q", path, want)
	}

	log.Print("hello-from-the-regression-test")
	closeLog()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(b), "hello-from-the-regression-test") {
		t.Errorf("log file does not contain the logged line, got:\n%s", b)
	}
}

func TestSetupLoggingIn_EmptyBaseIsNoop(t *testing.T) {
	path, closeLog := setupLoggingIn("")
	if path != "" {
		t.Errorf("path = %q, want \"\" (no writable location)", path)
	}
	closeLog() // must not panic
}

// TestStartupErrorMessage_IsActionable: the bare "XMAIL_API_KEY is
// required" a user would otherwise never see is not enough to act on —
// the dialog must say what to create and where.
func TestStartupErrorMessage_IsActionable(t *testing.T) {
	logPath := `C:\Users\someone\AppData\Local\xmail\xmail-tray.log`
	msg := startupErrorMessage(logPath, errors.New("config: XMAIL_API_KEY is required (static API key for X-API-Key auth)"))

	for _, want := range []string{
		"XMAIL_API_KEY is required", // the underlying error survives
		"XMAIL_ENCRYPTION_KEY",      // the other required var is named
		".env",                      // where to put it
		"openssl rand -base64 32",   // how to generate the key
		logPath,                     // so the user can find the log
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("startup message does not mention %q; got:\n%s", want, msg)
		}
	}
}

func TestStartupErrorMessage_OmitsLogWhenUnavailable(t *testing.T) {
	msg := startupErrorMessage("", errors.New("boom"))
	if strings.Contains(msg, "Log:") {
		t.Errorf("message claims a log path when none exists:\n%s", msg)
	}
	if !strings.Contains(msg, "boom") {
		t.Errorf("message lost the underlying error:\n%s", msg)
	}
}

// TestDialogAllowed_SuppressedByEnv: an automated run (CI, smoke test)
// must be able to keep a modal dialog from blocking it forever.
func TestDialogAllowed_SuppressedByEnv(t *testing.T) {
	t.Setenv(noDialogEnv, "1")
	if dialogAllowed() {
		t.Errorf("dialogAllowed() = true with %s=1, want false", noDialogEnv)
	}

	// Without the opt-out it follows whether a desktop exists at all.
	t.Setenv(noDialogEnv, "")
	if got, want := dialogAllowed(), winservice.Interactive(); got != want {
		t.Errorf("dialogAllowed() = %v, want %v (winservice.Interactive())", got, want)
	}
}
