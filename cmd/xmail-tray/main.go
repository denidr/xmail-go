//go:build windows && xmailtray

// Command xmail-tray is the Windows entrypoint: runs xmail as a system
// tray application (fyne.io/systray) with menu actions to
// install/start/stop/uninstall itself as a native Windows Service (via
// internal/winservice) and to open the local API address in the
// default browser. When launched by the Windows Service Control
// Manager (i.e. not interactively — see winservice.Interactive), it
// skips the tray UI entirely and runs as a plain background service.
// See PLAN.md "Release Build — Windows x64".
//
// Excluded from the default build (custom tag "xmailtray" must be
// passed explicitly, see Makefile release-windows-amd64) so the Docker
// release targets never need Windows-only/GUI dependencies.
package main

import (
	"context"
	"embed"
	"log"
	"os/exec"
	"sync"

	"fyne.io/systray"

	"xmail/internal/app"
	"xmail/internal/config"
	"xmail/internal/winservice"
)

//go:embed icon.ico
var assetsFS embed.FS

// version is set at build time via -ldflags "-X main.version=vX.Y.Z"
// (see scripts/release.sh). Left as "dev" for plain `go build`.
var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("xmail-tray: version=%s", version)

	if !winservice.Interactive() {
		// Launched by the Windows Service Control Manager: no tray UI,
		// just hand control to kardianos/service, which calls
		// program.Start/Stop (see internal/winservice) as the SCM
		// directs.
		svc, err := winservice.New(cfg, version)
		if err != nil {
			log.Fatalf("winservice: %v", err)
		}
		if err := svc.Run(); err != nil {
			log.Fatalf("winservice: run: %v", err)
		}
		return
	}

	t := &trayApp{cfg: cfg, version: version}
	systray.Run(t.onReady, t.onExit)
}

// trayApp holds the foreground (non-service) run state controlled by
// the tray menu's Start/Stop items.
type trayApp struct {
	cfg     config.Config
	version string

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
}

func (t *trayApp) onReady() {
	if iconBytes, err := assetsFS.ReadFile("icon.ico"); err == nil {
		systray.SetIcon(iconBytes)
	} else {
		log.Printf("xmail-tray: load icon: %v", err)
	}
	systray.SetTitle("xmail")
	systray.SetTooltip("xmail — email service")

	mStatus := systray.AddMenuItem("Status: stopped", "")
	mStatus.Disable()
	systray.AddSeparator()

	mStart := systray.AddMenuItem("Start", "Run xmail in this tray process")
	mStop := systray.AddMenuItem("Stop", "Stop the running xmail instance")
	mStop.Disable()
	systray.AddSeparator()

	mInstall := systray.AddMenuItem("Install as Windows Service", "Register xmail to run in the background, even when logged out")
	mUninstall := systray.AddMenuItem("Uninstall Windows Service", "")
	systray.AddSeparator()

	mDashboard := systray.AddMenuItem("Open dashboard", "Open the xmail API address in your browser")
	systray.AddSeparator()

	mQuit := systray.AddMenuItem("Quit", "Stop xmail (if running) and exit")

	go func() {
		for {
			select {
			case <-mStart.ClickedCh:
				t.start()
				mStatus.SetTitle("Status: running")
				mStart.Disable()
				mStop.Enable()

			case <-mStop.ClickedCh:
				t.stop()
				mStatus.SetTitle("Status: stopped")
				mStart.Enable()
				mStop.Disable()

			case <-mInstall.ClickedCh:
				t.installService()

			case <-mUninstall.ClickedCh:
				t.uninstallService()

			case <-mDashboard.ClickedCh:
				openBrowser("http://localhost" + t.cfg.ListenAddr)

			case <-mQuit.ClickedCh:
				t.stop()
				systray.Quit()
				return
			}
		}
	}()
}

func (t *trayApp) onExit() {
	t.stop()
}

func (t *trayApp) start() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.running {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	t.running = true
	go func() {
		if err := app.Run(ctx, t.cfg, t.version); err != nil {
			log.Printf("xmail-tray: app.Run stopped with error: %v", err)
		}
	}()
}

func (t *trayApp) stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.running {
		return
	}
	t.cancel()
	t.running = false
}

func (t *trayApp) installService() {
	svc, err := winservice.New(t.cfg, t.version)
	if err != nil {
		log.Printf("xmail-tray: build service descriptor: %v", err)
		return
	}
	if err := svc.Install(); err != nil {
		log.Printf("xmail-tray: install service failed: %v", err)
		return
	}
	log.Println("xmail-tray: Windows Service installed (see services.msc)")
}

func (t *trayApp) uninstallService() {
	svc, err := winservice.New(t.cfg, t.version)
	if err != nil {
		log.Printf("xmail-tray: build service descriptor: %v", err)
		return
	}
	if err := svc.Uninstall(); err != nil {
		log.Printf("xmail-tray: uninstall service failed: %v", err)
		return
	}
	log.Println("xmail-tray: Windows Service uninstalled")
}

// openBrowser shells out to the OS default-browser handler. Best
// effort only — failures are logged, not surfaced to the user (no
// dialog support in this MVP, see PLAN.md Fase 7 backlog note).
func openBrowser(url string) {
	if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err != nil {
		log.Printf("xmail-tray: open browser: %v", err)
	}
}
