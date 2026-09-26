// Package config loads xmail runtime configuration from environment
// variables (12-factor style). See PLAN.md Fase 1.
package config

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// Config holds all runtime settings for the xmail service.
type Config struct {
	ListenAddr     string // XMAIL_LISTEN_ADDR, e.g. ":8080"
	DBPath         string // XMAIL_DB_PATH, e.g. "xmail.db"
	EncryptionKey  []byte // decoded from XMAIL_ENCRYPTION_KEY, must be 32 bytes (AES-256)
	APIKey         string // XMAIL_API_KEY, MVP single static key
	MCPStdioEnable bool   // XMAIL_MCP_STDIO, expose MCP server over stdio
}

const (
	envListenAddr    = "XMAIL_LISTEN_ADDR"
	envDBPath        = "XMAIL_DB_PATH"
	envEncryptionKey = "XMAIL_ENCRYPTION_KEY"
	envAPIKey        = "XMAIL_API_KEY"
	envMCPStdio      = "XMAIL_MCP_STDIO"

	defaultListenAddr = ":8080"
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
		ListenAddr: getEnvOr(envListenAddr, defaultListenAddr),
		DBPath:     getEnvOr(envDBPath, defaultDBPath),
		APIKey:     os.Getenv(envAPIKey),
	}

	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("%s is required (static API key for X-API-Key auth)", envAPIKey)
	}

	rawKey := os.Getenv(envEncryptionKey)
	if rawKey == "" {
		return Config{}, fmt.Errorf("%s is required (base64-encoded 32-byte AES-256 key, generate with: openssl rand -base64 32)", envEncryptionKey)
	}
	key, err := base64.StdEncoding.DecodeString(rawKey)
	if err != nil {
		return Config{}, fmt.Errorf("%s must be valid base64: %w", envEncryptionKey, err)
	}
	if len(key) != encryptionKeyLen {
		return Config{}, fmt.Errorf("%s must decode to exactly %d bytes, got %d", envEncryptionKey, encryptionKeyLen, len(key))
	}
	cfg.EncryptionKey = key

	if v := os.Getenv(envMCPStdio); v == "true" || v == "1" {
		cfg.MCPStdioEnable = true
	}

	return cfg, nil
}

func getEnvOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
