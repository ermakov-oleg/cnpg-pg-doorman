package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/o-ermakov/cnpg-pg-doorman/internal/wrapper"
)

const (
	configMapPath = "/etc/pg_doorman/configmap/pg_doorman.yaml"
	runtimeConfig = "/tmp/pg_doorman.yaml"
	pollInterval  = 5 // seconds
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Validate and copy initial config
	if err := wrapper.ValidateAndCopyConfig(configMapPath, runtimeConfig, logger); err != nil {
		logger.Error("initial config invalid, waiting for valid config", "error", err)
		wrapper.WaitForValidConfig(ctx, configMapPath, runtimeConfig, pollInterval, logger)
		if ctx.Err() != nil {
			os.Exit(0)
		}
	}

	// Start pg_doorman with restart
	proc := wrapper.NewProcess(runtimeConfig, logger)
	go func() {
		if err := proc.RunWithRestart(ctx); err != nil && ctx.Err() == nil {
			logger.Error("pg_doorman run failed", "error", err)
			os.Exit(1)
		}
	}()

	// Watch for config changes
	watcher := wrapper.NewWatcher(configMapPath, runtimeConfig, proc, logger)
	go watcher.Run(ctx, pollInterval)

	<-ctx.Done()
	logger.Info("shutting down")
	_ = proc.Stop()
}
