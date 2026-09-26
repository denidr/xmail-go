// Package config loads xmail runtime configuration from environment
// variables (12-factor style). See PLAN.md Fase 1.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds all runtime settings for the xmail service.
type Config struct {
	ListenAddr     string // XMAIL_LISTEN_ADDR, e.g. ":5569"
	DBPath         string // XMAIL_DB_PATH, e.g. "xmail.db"
	EncryptionKey  []byte // decoded from XMAIL_ENCRYPTION_KEY, must be 32 bytes (AES-256)
	APIKey         string // XMAIL_API_KEY, MVP single static key
	MCPStdioEnable bool   // XMAIL_MCP_STDIO, expose MCP server over stdio
}

// Exported so a caller that has to reconstruct xmail's environment from
// a loaded Config (internal/winservice, which the Windows SCM launches
// into an empty environment) can name the variables without re-listing
// them — see Config.Environ.
const (
	EnvListenAddr    = "XMAIL_LISTEN_ADDR"
	EnvDBPath        = "XMAIL_DB_PATH"
	EnvEncryptionKey = "XMAIL_ENCRYPTION_KEY"
	EnvAPIKey        = "XMAIL_API_KEY"
	EnvMCPStdio      = "XMAIL_MCP_STDIO"

	defaultListenAddr = ":5569"
	defaultDBPath     = "xmail.db"

	// encryptionKeyLen is the required decoded length of
	// XMAIL_ENCRYPTION_KEY: 32 bytes for AES-256.
	encryptionKeyLen = 32
)

// Load reads configuration from environment variables, applying
// defaults for optional fields and returning an error listing what is
// missing/invalid for required ones.
//
// Before reading os.Getenv, it best-effort loads a .env file from the
// working directory via godotenv — dev convenience only (see
// PLAN.md §0). godotenv.Load never overwrites a variable that's
// already set in the real environment, so this has zero effect in
// Docker/production where .env is never present (excluded via
// .dockerignore) and/or real env vars are already set; a missing .env
// file is not an error, it just means "nothing to layer in".
func Load() (Config, error) {
	_ = godotenv.Load()

	cfg := Config{
		ListenAddr: getEnvOr(EnvListenAddr, defaultListenAddr),
		DBPath:     getEnvOr(EnvDBPath, defaultDBPath),
		APIKey:     os.Getenv(EnvAPIKey),
	}

	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("%s is required (static API key for X-API-Key auth)", EnvAPIKey)
	}

	rawKey := os.Getenv(EnvEncryptionKey)
	if rawKey == "" {
		return Config{}, fmt.Errorf("%s is required (base64-encoded 32-byte AES-256 key, generate with: openssl rand -base64 32)", EnvEncryptionKey)
	}
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil {
		return Config{}, fmt.Errorf("%s must be valid base64: %w", EnvEncryptionKey, err)
	}
	if len(key) != encryptionKeyLen {
		return Config{}, fmt.Errorf("%s must decode to exactly %d bytes, got %d", EnvEncryptionKey, encryptionKeyLen, len(key))
	}
	cfg.EncryptionKey = key

	if v := os.Getenv(EnvMCPStdio); v == "true" || v == "1" {
		cfg.MCPStdioEnable = true
	}

	return cfg, nil
}

// Environ returns cfg in the environment-variable form Load reads — the
// inverse of Load, so the set of XMAIL_* names and their encoding live
// only in this package (internal/winservice used to re-list them all as
// literals and re-encode EncryptionKey itself; that coupling shipped
// two bugs — see PLAN.md §10.6 #19 and §10.7 #A).
func (c Config) Environ() map[string]string {
	return map[string]string{
		EnvAPIKey:        c.APIKey,
		EnvEncryptionKey: base64.StdEncoding.EncodeToString(c.EncryptionKey),
		EnvListenAddr:    c.ListenAddr,
		EnvDBPath:        c.DBPath,
		EnvMCPStdio:      strconv.FormatBool(c.MCPStdioEnable),
	}
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
