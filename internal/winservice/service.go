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
	"fmt"
	"path/filepath"
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
// EnvVars is set from cfg via Config.Environ (see internal/config)
// because the Windows Service Control Manager launches the service in a
// *fresh* process with an empty environment — it does not inherit
// whatever environment the interactive tray process had when the user
// clicked "Install as Windows Service". Without this, the SCM-launched
// process's own config.Load() call (see cmd/xmail-tray/main.go) fails
// immediately for a missing XMAIL_API_KEY/XMAIL_ENCRYPTION_KEY and the
// installed service can never actually start — kardianos does support
// EnvVars on Windows (writes them into the service's registry entry),
// we just weren't setting it. Caught by code review; see PLAN.md §10.6.
func New(cfg config.Config, version string) (service.Service, error) {
	svcConfig, err := buildServiceConfig(cfg)
	if err != nil {
		return nil, err
	}
	return service.New(&program{cfg: cfg, version: version}, svcConfig)
}

// buildServiceConfig is split out from New so the EnvVars it produces
// can be unit-tested (round-tripped back through config.Load) without
// needing an actual Windows Service install/SCM, which requires
// Administrator privileges — see service_test.go.
//
// The set of variables and their encoding is Config.Environ's job, not
// this package's (previously winservice re-listed every XMAIL_* name as
// a literal and re-encoded EncryptionKey itself — the duplication that
// shipped the bug above). The one addition here is platform-specific:
// DBPath is resolved to an absolute path before being written to
// EnvVars. The Windows Service Control Manager launches services with
// cwd %SystemRoot%\System32 (kardianos/service's WorkingDirectory
// field is explicitly "not supported on Windows", so it can't fix
// this), so a relative XMAIL_DB_PATH (the documented default,
// .env.example: "xmail.db") would have the installed service try to
// open/create its database under System32 instead of wherever the
// interactive session actually meant — access-denied at best, a
// database silently diverged from the tray's at worst. Resolving it
// here, relative to the cwd of the interactive process installing the
// service (a sane, predictable location), fixes that. Caught by
// code review; see PLAN.md §10.7.
func buildServiceConfig(cfg config.Config) (*service.Config, error) {
	env := cfg.Environ()

	if cfg.DBPath == "" {
		// config.Load always supplies a default, so this is a hand-built
		// Config: filepath.Abs("") is the cwd, which would leave the service
		// trying to open a directory. Fail loudly instead.
		return nil, fmt.Errorf("winservice: XMAIL_DB_PATH is empty")
	}
	absDBPath, err := filepath.Abs(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("winservice: resolve absolute XMAIL_DB_PATH: %w", err)
	}
	env[config.EnvDBPath] = absDBPath

	return &service.Config{
		Name:        Name,
		DisplayName: DisplayName,
		Description: Description,
		EnvVars:     env,
	}, nil
}

// Interactive reports whether the current process is running
// interactively (double-clicked / launched from a shell) as opposed
// to under the Windows Service Control Manager.
func Interactive() bool {
	return service.Interactive()
}
