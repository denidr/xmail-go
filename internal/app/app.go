// Package app wires config, storage, account service, mailer
// implementations, and starts the API + MCP servers. It is the single
// shared entrypoint used by both release targets:
//   - cmd/xmail        (headless — Docker x64 / Docker Armbian(arm64))
//   - cmd/xmail-tray   (Windows x64 — system tray + Windows Service)
//
// Keeping this logic here (instead of duplicated in each cmd/) means the
// two distribution targets can never drift in behavior. See PLAN.md
// Fase 1 and the "Release Build" section.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"xmail/internal/account"
	"xmail/internal/api"
	"xmail/internal/config"
	"xmail/internal/mailer"
	"xmail/internal/mailer/imap"
	"xmail/internal/mailer/pop3"
	"xmail/internal/mailer/smtp"
	"xmail/internal/mcpserver"
	"xmail/internal/storage"
)

// shutdownTimeout bounds how long Run waits for in-flight requests to
// finish after ctx is cancelled.
const shutdownTimeout = 10 * time.Second

// Run wires all dependencies (storage, account service, mailer
// clients, API + MCP servers) and blocks serving until ctx is
// cancelled, then shuts down gracefully. version is the build-stamped
// app version (see cmd/xmail's/cmd/xmail-tray's `version` var) —
// threaded through to mcpserver.New so MCP clients see the real build
// version during the initialize handshake, not a separate hardcoded
// one (see CODE_REVIEW.md).
func Run(ctx context.Context, cfg config.Config, version string) error {
	db, err := storage.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("app: open storage: %w", err)
	}
	defer db.Close()

	repo := account.NewRepository(db, cfg.EncryptionKey)
	svc := account.NewService(repo)
	wireMailer(svc)

	mcpSrv := mcpserver.New(svc, version)
	srv := api.NewServer(cfg.APIKey, svc, mcpSrv.HTTPHandler())
	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv.Handler(),
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("xmail: API + MCP (HTTP, /mcp) listening on %s", cfg.ListenAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("app: http server: %w", err)
			return
		}
		errCh <- nil
	}()

	if cfg.MCPStdioEnable {
		go func() {
			log.Println("xmail: MCP stdio transport enabled")
			if err := mcpSrv.ServeStdio(); err != nil {
				log.Printf("xmail: MCP stdio server stopped: %v", err)
			}
		}()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	log.Println("xmail: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("app: shutdown: %w", err)
	}
	<-errCh
	return nil
}

// wireMailer plugs each protocol's mailer implementation into svc as
// it becomes available (Fase 2: SMTP, Fase 3: IMAP, Fase 4: POP3) —
// see PLAN.md §1 design principle: svc only ever depends on the
// mailer/account interfaces, never on the concrete protocol packages
// directly, so callers (internal/api, internal/mcpserver) stay
// protocol-agnostic.
func wireMailer(svc *account.Service) {
	svc.SetSMTPTester(func(cfg account.ConnectionConfig, username, secret string) account.ConnTester {
		return smtp.New(cfg, "", username, secret)
	})
	svc.SetSMTPSender(func(cfg account.ConnectionConfig, fromAddress, username, secret string) mailer.Sender {
		return smtp.New(cfg, fromAddress, username, secret)
	})
	svc.SetIMAPFactory(func(cfg account.ConnectionConfig, username, secret string) mailer.FetcherChecker {
		return imap.New(cfg, username, secret)
	})
	svc.SetIMAPTester(func(cfg account.ConnectionConfig, username, secret string) account.ConnTester {
		return imap.New(cfg, username, secret)
	})
	svc.SetPOP3Factory(func(cfg account.ConnectionConfig, username, secret string) mailer.Fetcher {
		return pop3.New(cfg, username, secret)
	})
	svc.SetPOP3Tester(func(cfg account.ConnectionConfig, username, secret string) account.ConnTester {
		return pop3.New(cfg, username, secret)
	})
}
