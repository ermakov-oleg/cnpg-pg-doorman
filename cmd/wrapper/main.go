package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/wrapper"
)

const (
	pollIntervalSec       = 2
	binaryPollIntervalSec = 10
)

// The wrapper has no Kubernetes client: the plugin controller renders the
// config into a per-cluster Secret, mounted into this pod. The wrapper only
// supervises pg_doorman, applies mounted config changes, watches the
// instance role, and serves its own health endpoint.
func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: wrapper.ParseLogLevel(os.Getenv("LOG_LEVEL")),
	}))
	wrapper.SetChildSubreaper(logger)
	wrapper.CapFileDescriptorLimit(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	healthPort := envInt("HEALTH_PORT", 8081, logger)
	sourcePath := envOr("CONFIG_SOURCE", wrapper.ConfigSourcePath)
	rawKeyPath := os.Getenv("TLS_KEY_PATH")

	// The kubelet liveness probe targets this endpoint: it reflects wrapper
	// liveness only, never pg_doorman state (see internal/lifecycle).
	go serveHealth(healthPort, logger)

	proc := wrapper.NewProcess(wrapper.RuntimeConfigPath, logger)
	fw := wrapper.NewFileWatcher(sourcePath, wrapper.RuntimeConfigPath, rawKeyPath, wrapper.ConvertedTLSKeyPath, proc, logger)

	specSource := envOr("BINARY_SPEC_SOURCE", wrapper.BinarySpecSourcePath)
	syncer := wrapper.NewBinarySyncer(
		specSource, wrapper.ImageBinaryPath, wrapper.RuntimeBinaryPath, runtime.GOARCH, logger)
	appliedSpec, err := syncer.EnsureAtStartup(ctx)
	if err != nil {
		logger.Error("initial binary sync failed", "error", err)
		os.Exit(1)
	}

	if err := fw.ApplyInitial(ctx); err != nil {
		// The Secret volume is mounted before the container starts, so this
		// only fails on a genuinely broken rendered config.
		logger.Error("initial config apply failed", "error", err)
		os.Exit(1)
	}

	procDone := make(chan struct{})
	go func() {
		defer close(procDone)
		if err := proc.RunWithRestart(ctx); err != nil && ctx.Err() == nil {
			logger.Error("pg_doorman run failed", "error", err)
			os.Exit(1)
		}
	}()

	go fw.Run(ctx, pollIntervalSec)

	// A binary change is rare and the spec file is tiny: a slow poll is enough.
	bw := wrapper.NewBinaryWatcher(specSource, syncer, proc, logger)
	bw.Seed(appliedSpec)
	go bw.Run(ctx, binaryPollIntervalSec)

	// Watch the instance role and drop pooler sessions on demotion
	if roleFile := os.Getenv("ROLE_FILE"); roleFile != "" {
		rw := wrapper.NewRoleWatcher(roleFile, proc, logger)
		go rw.Run(ctx, 5)
	}

	<-ctx.Done()
	logger.Info("shutting down, waiting for pg_doorman to exit")
	// The wrapper is PID 1: exiting main kills the container along with pg_doorman,
	// so wait until RunWithRestart observes the process exit.
	<-procDone
}

func serveHealth(port int, logger *slog.Logger) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.Handle("/metrics", wrapper.MetricsHandler())
	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		logger.Error("health server failed", "error", err)
		os.Exit(1)
	}
}

func envOr(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func envInt(key string, defaultVal int, logger *slog.Logger) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		logger.Warn("invalid integer env var, using default", "key", key, "value", v, "default", defaultVal, "error", err)
		return defaultVal
	}
	return i
}
