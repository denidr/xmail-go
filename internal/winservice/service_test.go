//go:build windows && xmailtray

package winservice

import (
	"testing"

	"xmail/internal/config"
)

// TestBuildServiceConfig_EnvVarsRoundTrip is the regression test for a
// real bug: New() built a service.Config with no EnvVars at all, so a
// service installed via it would be launched by the Windows Service
// Control Manager in a *fresh* process with none of
// XMAIL_API_KEY/XMAIL_ENCRYPTION_KEY set — that process's own
// config.Load() call (cmd/xmail-tray/main.go) would fail immediately,
// meaning an installed service could never actually start. Installing
// a real service requires Administrator and isn't something this test
// (or an agent without elevation) can do, so instead this simulates
// exactly the failure mode: take the EnvVars buildServiceConfig
// produces, set *only* those (clearing everything else first, as a
// fresh SCM-launched process would have), and confirm config.Load()
// succeeds and reproduces the original config.
func TestBuildServiceConfig_EnvVarsRoundTrip(t *testing.T) {
	// Simulate the interactive session the user installs the service
	// from: real env vars set, config.Load() succeeds.
	t.Setenv("XMAIL_API_KEY", "test-api-key")
	t.Setenv("XMAIL_ENCRYPTION_KEY", "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=") // 32 bytes
	t.Setenv("XMAIL_LISTEN_ADDR", ":9999")
	t.Setenv("XMAIL_DB_PATH", "custom.db")
	t.Setenv("XMAIL_MCP_STDIO", "")

	original, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() (interactive session) error = %v", err)
	}

	svcConfig := buildServiceConfig(original)

	// Simulate the SCM launching the installed service: a fresh process
	// with nothing inherited from the interactive session — clear
	// everything, then set *only* what buildServiceConfig put in
	// EnvVars (this is what kardianos injects on Windows).
	t.Setenv("XMAIL_API_KEY", "")
	t.Setenv("XMAIL_ENCRYPTION_KEY", "")
	t.Setenv("XMAIL_LISTEN_ADDR", "")
	t.Setenv("XMAIL_DB_PATH", "")
	for k, v := range svcConfig.EnvVars {
		t.Setenv(k, v)
	}

	reloaded, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() in simulated SCM environment (only EnvVars set) error = %v — an installed service would fail to start with this error", err)
	}

	if reloaded.APIKey != original.APIKey {
		t.Errorf("APIKey = %q, want %q", reloaded.APIKey, original.APIKey)
	}
	if string(reloaded.EncryptionKey) != string(original.EncryptionKey) {
		t.Errorf("EncryptionKey mismatch after EnvVars round-trip")
	}
	if reloaded.ListenAddr != original.ListenAddr {
		t.Errorf("ListenAddr = %q, want %q", reloaded.ListenAddr, original.ListenAddr)
	}
	if reloaded.DBPath != original.DBPath {
		t.Errorf("DBPath = %q, want %q", reloaded.DBPath, original.DBPath)
	}
}
