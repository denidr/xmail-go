//go:build windows && xmailtray

package winservice

import (
	"path/filepath"
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
	t.Setenv("XMAIL_MCP_STDIO", "true")

	original, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() (interactive session) error = %v", err)
	}

	svcConfig, err := buildServiceConfig(original)
	if err != nil {
		t.Fatalf("buildServiceConfig() error = %v", err)
	}

	// Simulate the SCM launching the installed service: a fresh process
	// with nothing inherited from the interactive session — clear
	// everything, then set *only* what buildServiceConfig put in
	// EnvVars (this is what kardianos injects on Windows).
	t.Setenv("XMAIL_API_KEY", "")
	t.Setenv("XMAIL_ENCRYPTION_KEY", "")
	t.Setenv("XMAIL_LISTEN_ADDR", "")
	t.Setenv("XMAIL_DB_PATH", "")
	t.Setenv("XMAIL_MCP_STDIO", "")
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
	// DBPath is deliberately NOT compared to original.DBPath verbatim —
	// buildServiceConfig absolutizes it (see TestBuildServiceConfig_AbsolutizesDBPath),
	// so it will legitimately differ from the original relative value.
	if !filepath.IsAbs(reloaded.DBPath) {
		t.Errorf("DBPath = %q, want an absolute path", reloaded.DBPath)
	}
	// Regression: XMAIL_MCP_STDIO used to be silently dropped from
	// EnvVars entirely — see PLAN.md §10.7.
	if !reloaded.MCPStdioEnable {
		t.Error("MCPStdioEnable = false after EnvVars round-trip, want true (XMAIL_MCP_STDIO must be propagated)")
	}
}

// TestBuildServiceConfig_AbsolutizesDBPath is the regression test for a
// real bug: a relative XMAIL_DB_PATH (the documented default, see
// .env.example) was passed through to EnvVars unchanged. The Windows
// Service Control Manager launches services with cwd
// %SystemRoot%\System32 — kardianos/service's WorkingDirectory option
// is explicitly unsupported on Windows, so nothing else can correct
// this — meaning an installed service with the default config would
// try to open/create its database under System32 instead of the
// location the interactive session actually meant.
func TestBuildServiceConfig_AbsolutizesDBPath(t *testing.T) {
	cfg := config.Config{
		APIKey:        "k",
		EncryptionKey: make([]byte, 32),
		ListenAddr:    ":5569",
		DBPath:        "xmail.db", // relative, matches .env.example's default
	}

	svcConfig, err := buildServiceConfig(cfg)
	if err != nil {
		t.Fatalf("buildServiceConfig() error = %v", err)
	}

	got := svcConfig.EnvVars["XMAIL_DB_PATH"]
	if !filepath.IsAbs(got) {
		t.Fatalf("EnvVars[XMAIL_DB_PATH] = %q, want an absolute path (relative would resolve under %%SystemRoot%%\\System32 when the SCM launches the service, not wherever the user meant)", got)
	}
	wantSuffix := string(filepath.Separator) + "xmail.db"
	if len(got) < len(wantSuffix) || got[len(got)-len(wantSuffix):] != wantSuffix {
		t.Errorf("EnvVars[XMAIL_DB_PATH] = %q, want it to still end in %q (just made absolute, not otherwise changed)", got, wantSuffix)
	}
}
