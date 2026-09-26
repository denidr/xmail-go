package config

import (
	"os"
	"path/filepath"
	"testing"
)

// clearEnv resets all xmail env vars to empty for the duration of the
// test (t.Setenv auto-restores after). An empty value is treated the
// same as unset by getEnvOr/Load, so this is sufficient without a real
// unsetenv syscall — EXCEPT for godotenv, which checks presence in
// os.Environ() (true even for a var set to ""), so tests that need
// godotenv to actually load a value from .env must use unsetEnv
// instead (see TestLoad_ReadsDotEnv).
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{EnvListenAddr, EnvDBPath, EnvEncryptionKey, EnvAPIKey, EnvMCPStdio} {
		t.Setenv(k, "")
	}
}

// unsetEnv truly removes key from the environment (not just sets it to
// ""), restoring its original value (or absence) after the test.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	orig, wasSet := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%s): %v", key, err)
	}
	t.Cleanup(func() {
		if wasSet {
			os.Setenv(key, orig)
		} else {
			os.Unsetenv(key)
		}
	})
}

func TestLoad_Defaults(t *testing.T) {
	clearEnv(t)
	t.Setenv(EnvAPIKey, "test-key")
	t.Setenv(EnvEncryptionKey, "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=") // 32 bytes base64

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != defaultListenAddr {
		t.Errorf("ListenAddr = %q, want default %q", cfg.ListenAddr, defaultListenAddr)
	}
	if cfg.DBPath != defaultDBPath {
		t.Errorf("DBPath = %q, want default %q", cfg.DBPath, defaultDBPath)
	}
	if len(cfg.EncryptionKey) != encryptionKeyLen {
		t.Errorf("EncryptionKey len = %d, want %d", len(cfg.EncryptionKey), encryptionKeyLen)
	}
}

func TestLoad_MissingAPIKey(t *testing.T) {
	clearEnv(t)
	t.Setenv(EnvEncryptionKey, "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error for missing API key")
	}
}

func TestLoad_MissingEncryptionKey(t *testing.T) {
	clearEnv(t)
	t.Setenv(EnvAPIKey, "test-key")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error for missing encryption key")
	}
}

func TestLoad_InvalidEncryptionKeyLength(t *testing.T) {
	clearEnv(t)
	t.Setenv(EnvAPIKey, "test-key")
	t.Setenv(EnvEncryptionKey, "dG9vc2hvcnQ=") // "tooshort", not 32 bytes

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error for wrong-length key")
	}
}

func TestLoad_InvalidEncryptionKeyBase64(t *testing.T) {
	clearEnv(t)
	t.Setenv(EnvAPIKey, "test-key")
	t.Setenv(EnvEncryptionKey, "not-valid-base64!!!")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want error for invalid base64")
	}
}

// TestLoad_ReadsDotEnv is the regression test for PLAN.md §0's promise
// that ".env opsional untuk dev via godotenv" actually does something —
// a prior version had no godotenv import at all, so README.MD's
// documented "cp .env.example .env; make run" flow silently did
// nothing (Load only ever read the real process environment). This
// writes a real .env file into a temp working directory (godotenv.Load
// reads ".env" relative to the process cwd) and confirms Load() picks
// it up.
func TestLoad_ReadsDotEnv(t *testing.T) {
	for _, k := range []string{EnvListenAddr, EnvDBPath, EnvEncryptionKey, EnvAPIKey, EnvMCPStdio} {
		unsetEnv(t, k)
	}

	dir := t.TempDir()
	dotenv := "XMAIL_API_KEY=from-dotenv\nXMAIL_ENCRYPTION_KEY=MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=\nXMAIL_LISTEN_ADDR=:9191\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWD) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want values picked up from .env", err)
	}
	if cfg.APIKey != "from-dotenv" {
		t.Errorf("APIKey = %q, want %q (from .env)", cfg.APIKey, "from-dotenv")
	}
	if cfg.ListenAddr != ":9191" {
		t.Errorf("ListenAddr = %q, want %q (from .env)", cfg.ListenAddr, ":9191")
	}
}

// TestLoad_RealEnvOverridesDotEnv confirms an already-set real env var
// wins over .env — the documented, expected precedence (.env is a
// fallback for local dev convenience, never an override of an
// explicitly configured environment such as Docker's -e/--env-file).
func TestLoad_RealEnvOverridesDotEnv(t *testing.T) {
	for _, k := range []string{EnvListenAddr, EnvDBPath, EnvEncryptionKey, EnvAPIKey, EnvMCPStdio} {
		unsetEnv(t, k)
	}

	dir := t.TempDir()
	dotenv := "XMAIL_API_KEY=from-dotenv\nXMAIL_ENCRYPTION_KEY=MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWD) })

	t.Setenv(EnvAPIKey, "from-real-env")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.APIKey != "from-real-env" {
		t.Errorf("APIKey = %q, want %q (real env must win over .env)", cfg.APIKey, "from-real-env")
	}
}

// TestEnviron_RoundTripsThroughLoad locks in that Environ is the
// inverse of Load: a Config serialized to its environment form and read
// back through Load reproduces the same Config. This is what lets
// internal/winservice reconstruct a service process's environment from
// a loaded Config instead of re-listing every XMAIL_* name itself (the
// duplication that shipped two bugs — see PLAN.md §10.6/§10.7).
func TestEnviron_RoundTripsThroughLoad(t *testing.T) {
	clearEnv(t)
	t.Setenv(EnvAPIKey, "test-key")
	t.Setenv(EnvEncryptionKey, "MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")
	t.Setenv(EnvListenAddr, ":9999")
	t.Setenv(EnvDBPath, "custom.db")
	t.Setenv(EnvMCPStdio, "true")

	original, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Simulate a fresh process whose entire environment is Environ's
	// output (nothing inherited).
	clearEnv(t)
	for k, v := range original.Environ() {
		t.Setenv(k, v)
	}

	reloaded, err := Load()
	if err != nil {
		t.Fatalf("Load() after round-trip error = %v", err)
	}
	if reloaded.APIKey != original.APIKey {
		t.Errorf("APIKey = %q, want %q", reloaded.APIKey, original.APIKey)
	}
	if string(reloaded.EncryptionKey) != string(original.EncryptionKey) {
		t.Error("EncryptionKey mismatch after Environ round-trip")
	}
	if reloaded.ListenAddr != original.ListenAddr {
		t.Errorf("ListenAddr = %q, want %q", reloaded.ListenAddr, original.ListenAddr)
	}
	if reloaded.DBPath != original.DBPath {
		t.Errorf("DBPath = %q, want %q", reloaded.DBPath, original.DBPath)
	}
	if reloaded.MCPStdioEnable != original.MCPStdioEnable {
		t.Errorf("MCPStdioEnable = %v, want %v", reloaded.MCPStdioEnable, original.MCPStdioEnable)
	}
}
