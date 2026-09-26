//go:build windows && xmailtray

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"xmail/internal/winservice"
)

// This file exists to make a failed startup *visible*.
//
// The release build is a windowsgui executable (`-H=windowsgui`, see
// scripts/release.sh), so it has no console and its stderr goes nowhere
// when launched from Explorer. Before this, a first run with no
// configuration looked like nothing happening at all: config.Load fails
// -> log.Fatalf -> os.Exit(1), with no tray icon, no Task Manager entry
// and nothing in services.msc to explain it. Two mechanisms fix that:
// every log line is also written to a file under %LOCALAPPDATA%\xmail\,
// and a fatal startup error additionally raises a message box when there
// is a user able to see it.
//
// See PLAN.md §10.10 (the bug) — verified by launching the release .exe
// with no environment: previously silent exit 1, now a dialog plus a log.

const (
	mbOK            = 0x00000000
	mbIconError     = 0x00000010
	mbSetForeground = 0x00010000

	// noDialogEnv disables the message box. A modal dialog has nobody to
	// dismiss it in an automated run (CI, the smoke test in
	// scripts/test.*), so those set XMAIL_NO_DIALOG=1 and read the log
	// file instead.
	noDialogEnv = "XMAIL_NO_DIALOG"

	logFileName = "xmail-tray.log"
)

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

// setupLogging starts mirroring the standard logger to a file, so a
// windowsgui build leaves a trail after it exits. Returns the log path
// ("" when no writable location was found — logging then stays on stderr
// only) and a closer.
func setupLogging() (string, func()) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", func() {}
	}
	return setupLoggingIn(base)
}

// setupLoggingIn is setupLogging against an explicit base directory, so
// the behavior is testable without touching the real user profile.
func setupLoggingIn(base string) (string, func()) {
	if base == "" {
		return "", func() {}
	}
	dir := filepath.Join(base, "xmail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", func() {}
	}
	path := filepath.Join(dir, logFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", func() {}
	}
	// stderr too, which is useful when the process is started with
	// redirection (smoke tests do exactly that to capture the message).
	// Writes are best-effort: io.MultiWriter stops at the first failing
	// writer, and an unattached stderr must not swallow the file line.
	log.SetOutput(bestEffortWriter{os.Stderr, f})
	return path, func() { f.Close() }
}

type bestEffortWriter []io.Writer

func (w bestEffortWriter) Write(p []byte) (int, error) {
	for _, dst := range w {
		_, _ = dst.Write(p)
	}
	return len(p), nil
}

// dialogAllowed reports whether a startup error can be shown to a human.
// Under the Service Control Manager there is no desktop to draw on, and
// automated runs opt out with XMAIL_NO_DIALOG=1 because a modal box
// would block them forever.
func dialogAllowed() bool {
	if os.Getenv(noDialogEnv) == "1" {
		return false
	}
	return winservice.Interactive()
}

// fatalStartup logs err, shows it if anyone can see it, and exits.
func fatalStartup(logPath, title string, err error) {
	log.Printf("xmail-tray: fatal: %v", err)
	if dialogAllowed() {
		messageBox(title, startupErrorMessage(logPath, err))
	}
	os.Exit(1)
}

// startupErrorMessage is the message-box body. It is deliberately
// actionable: the most common cause is a first run with no configuration
// at all, which is not obvious from a bare "XMAIL_API_KEY is required".
func startupErrorMessage(logPath string, err error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%v\n\n", err)
	b.WriteString("xmail membutuhkan XMAIL_API_KEY dan XMAIL_ENCRYPTION_KEY.\n\n")
	b.WriteString("Cara termudah: buat file bernama .env di folder yang sama dengan xmail.exe, berisi:\n\n")
	b.WriteString("  XMAIL_API_KEY=kunci-pilihanmu\n")
	b.WriteString("  XMAIL_ENCRYPTION_KEY=<base64 dari 32 byte acak>\n\n")
	b.WriteString("Buat nilai XMAIL_ENCRYPTION_KEY dengan:\n  openssl rand -base64 32\n")
	b.WriteString("(lihat .env.example di repo untuk daftar lengkap)")
	if logPath != "" {
		fmt.Fprintf(&b, "\n\nLog: %s", logPath)
	}
	return b.String()
}

// messageBox shows a modal error box owned by no window. Best-effort:
// if user32 cannot be reached there is nothing useful left to do.
func messageBox(title, text string) {
	t, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	m, err := syscall.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	_, _, _ = procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(m)),
		uintptr(unsafe.Pointer(t)),
		uintptr(mbOK|mbIconError|mbSetForeground))
}
