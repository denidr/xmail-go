// Command xmail is the headless entrypoint used by the Docker release
// targets (linux/amd64 and linux/arm64 / Armbian). For the Windows
// tray + service target, see cmd/xmail-tray.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"xmail/internal/app"
	"xmail/internal/config"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z"
// (see scripts/release.sh). Left as "dev" for plain `go build`/`go run`.
var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	log.Printf("xmail: starting (headless mode), version=%s", version)
	if err := app.Run(ctx, cfg, version); err != nil {
		log.Fatalf("xmail: %v", err)
	}
}
