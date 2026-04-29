// Command virgil-server is the GitHub App webhook receiver.
//
// It listens for push events, verifies the HMAC signature, fetches
// the compare-commits diff, runs the reviewer, and posts the result
// as a GitHub Check Run.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/saarsena/virgil-review/internal/webhook"
	"github.com/saarsena/virgil-review/pkg/config"
	"github.com/saarsena/virgil-review/pkg/ghclient"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "virgil-server: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "./config.yaml", "path to YAML config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	factory, err := ghclient.NewFactory(cfg.GitHub.AppID, cfg.GitHub.PrivateKeyPath)
	if err != nil {
		return fmt.Errorf("building github factory: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/webhook", webhook.New(cfg.GitHub.WebhookSecret, factory, logger))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("starting virgil-server", "addr", addr, "app_id", cfg.GitHub.AppID)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-stop:
		logger.Info("shutting down", "signal", sig.String())
	case err := <-errCh:
		return fmt.Errorf("listen: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
