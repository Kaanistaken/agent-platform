// Command server runs the agent platform's backend: the REST API, the SSE
// streams and the run workers, in one process (D84).
package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kaang/vodafone-case/backend/internal/api"
	"github.com/kaang/vodafone-case/backend/internal/auth"
	"github.com/kaang/vodafone-case/backend/internal/config"
	"github.com/kaang/vodafone-case/backend/internal/crypto"
	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/provider"
	"github.com/kaang/vodafone-case/backend/internal/provider/anthropic"
	"github.com/kaang/vodafone-case/backend/internal/provider/openai"
	"github.com/kaang/vodafone-case/backend/internal/run"
	"github.com/kaang/vodafone-case/backend/internal/tool"
)

func main() {
	if err := serve(); err != nil {
		log.Fatal(err)
	}
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, err := db.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}
	defer d.Close()

	box, err := crypto.New(cfg.MasterKey)
	if err != nil {
		return err
	}
	if err := box.VerifyMasterKey(ctx, d.W); err != nil {
		return err
	}
	if err := auth.Seed(ctx, d.W, auth.DefaultSeed); err != nil {
		return err
	}

	providers := map[string]provider.Provider{
		"openai":    openai.New(),
		"anthropic": anthropic.New(),
	}
	engine := run.New(d, box, providers, tool.NewClient())
	if err := engine.Start(ctx); err != nil {
		return err
	}

	// Cancelling baseCtx on shutdown also ends open SSE streams.
	baseCtx, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	srv := &http.Server{
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
		Addr:              cfg.Addr,
		Handler:           (&api.Server{DB: d, Box: box, Engine: engine, Origin: cfg.Origin}).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("API listening on http://%s (allowed origin %s)", cfg.Addr, cfg.Origin)
		errCh <- srv.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case <-stop:
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	log.Print("shutting down; runs resume on the next start")
	cancelBase()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	srv.Shutdown(shutdownCtx)
	engine.Stop()
	return nil
}
