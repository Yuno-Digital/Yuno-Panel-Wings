// Command yuno-wings is the node daemon for Yuno Panel. It exposes an
// authenticated HTTP API that the panel uses to control game-server containers
// on this host. Modeled on Pelican Wings, kept intentionally minimal for now.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yuno/wings/config"
	"github.com/yuno/wings/internal/docker"
	"github.com/yuno/wings/router"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// `yuno-wings configure --panel-url … --token … --node …` fetches this.
	// node's config (including the panel-owned token) and writes config.json.
	if len(os.Args) > 1 && os.Args[1] == "configure" {
		if err := runConfigure(os.Args[2:]); err != nil {
			log.Error("configure failed", "error", err)
			os.Exit(1)
		}
		log.Info("wrote config.json — start the daemon with: yuno-wings")
		return
	}

	cfg, created, err := config.Load(config.DefaultPath)
	if err != nil {
		log.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	if created {
		log.Info("wrote default config — review it and set your panel URL, then restart",
			"path", config.DefaultPath, "token", cfg.Token)
		return
	}

	dc := docker.New(cfg.DockerPrefix)
	if !dc.Available(context.Background()) {
		log.Warn("docker is not reachable; power actions will fail until it is running")
	}

	srv := &http.Server{
		Addr:              cfg.Address(),
		Handler:           router.New(cfg, dc, log),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Run the server until we receive an interrupt/terminate signal.
	go func() {
		log.Info("yuno-wings listening", "address", cfg.Address(), "version", router.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "error", err)
	}
}
