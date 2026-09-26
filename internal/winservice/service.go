//go:build windows && xmailtray

// Package winservice installs/controls xmail as a native Windows Service
// (via github.com/kardianos/service), so it can keep running in the
// background without a logged-in user or visible tray icon. Used by
// cmd/xmail-tray's "Install as Windows Service" / "Uninstall" / "Start" /
// "Stop" menu actions. See PLAN.md "Release Build — Windows x64".
//
// Excluded from the default build (custom tag "xmailtray" must be
// passed explicitly, see Makefile release-windows-amd64) so the Docker
// release targets never need this Windows-only dependency.
package winservice

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/kardianos/service"

	"xmail/internal/app"
	"xmail/internal/config"
)

const (
	Name        = "XmailService"
	DisplayName = "xmail Email Service"
	Description = "Multi-account email send/fetch service with MCP support."

	// stopTimeout bounds how long Stop waits for app.Run to return
	// before giving up — the Windows Service Control Manager kills the
	// process if Stop blocks too long (default SCM timeout is ~30s).
	stopTimeout = 10 * time.Second
)

// program adapts app.Run to the kardianos/service.Interface expected by
// the Windows Service Control Manager (Start must return quickly; the
// real work runs in a goroutine, Stop cancels it).
type program struct {
	cfg     config.Config
	version string
	cancel  context.CancelFunc
	done    chan error
}

func (p *program) Start(s service.Service) error {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.done = make(chan error, 1)

	go func() {
		p.done <- app.Run(ctx, p.cfg, p.version)
	}()
	return nil
}

func (p *program) Stop(s service.Service) error {
	if p.cancel == nil {
		return nil
	}
	p.cancel()

	select {
	case err := <-p.done:
		return err
	case <-time.After(stopTimeout):
		return fmt.Errorf("winservice: app.Run did not shut down within %s", stopTimeout)
	}
}

// New builds the kardianos service.Service descriptor used for
// Install/Uninstall/Start/Stop/Run against the Windows Service Control
// Manager.
//
// EnvVars is set from cfg (re-encoding EncryptionKey back to the same
// base64 form config.Load expects) because the Windows Service Control
// Manager launches the service in a *fresh* process with an empty
// environment — it does not inherit whatever environment the
// interactive tray process had when the user clicked "Install as
// Windows Service". Without this, the SCM-launched process's own
// config.Load() call (see cmd/xmail-tray/main.go) fails immediately
// for a missing XMAIL_API_KEY/XMAIL_ENCRYPTION_KEY and the installed
// service can never actually start — kardianos does support EnvVars on
// Windows (writes them into the service's registry entry), we just
// weren't setting it. Caught by code review; see PLAN.md §10.6.
func New(cfg config.Config, version string) (service.Service, error) {
	return service.New(&program{cfg: cfg, version: version}, buildServiceConfig(cfg))
}

// buildServiceConfig is split out from New so the EnvVars it produces
// can be unit-tested (round-tripped back through config.Load) without
// needing an actual Windows Service install/SCM, which requires
// Administrator privileges — see service_test.go.
func buildServiceConfig(cfg config.Config) *service.Config {
	return &service.Config{
		Name:        Name,
		DisplayName: DisplayName,
		Description: Description,
		EnvVars: map[string]string{
			"XMAIL_API_KEY":        cfg.APIKey,
			"XMAIL_ENCRYPTION_KEY": base64.StdEncoding.EncodeToString(cfg.EncryptionKey),
			"XMAIL_LISTEN_ADDR":    cfg.ListenAddr,
			"XMAIL_DB_PATH":        cfg.DBPath,
		},
	}
}

// Interactive reports whether the current process is running
// interactively (double-clicked / launched from a shell) as opposed
// to under the Windows Service Control Manager.
func Interactive() bool {
	return service.Interactive()
}
