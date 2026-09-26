//go:build xmailtray

package winservice

import (
	"testing"

	"xmail/internal/config"
)

// TestBuildServiceConfig_EmptyDBPathErrors guards a hand-built Config:
// filepath.Abs("") is the process cwd, so an empty path would leave the
// installed service trying to open a directory instead of a database.
func TestBuildServiceConfig_EmptyDBPathErrors(t *testing.T) {
	if _, err := buildServiceConfig(config.Config{}); err == nil {
		t.Fatal("buildServiceConfig(empty DBPath) error = nil, want an explicit error")
	}
}
