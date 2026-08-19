package wrapper

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"time"
)

// Reloader asks the running pg_doorman process to re-read its config, or to
// restart when the change touches fields SIGHUP cannot apply.
type Reloader interface {
	Reload() error
	Restart() error
}

// FileWatcher polls the rendered config mounted from the per-cluster Secret
// and applies changes to the running pg_doorman. The wrapper has no kube
// client: the plugin controller renders the config centrally.
type FileWatcher struct {
	sourcePath  string
	runtimePath string
	rawKeyPath  string
	keyPath     string
	process     Reloader
	testConfig  ConfigTester
	logger      *slog.Logger
	lastApplied []byte
}

// NewFileWatcher creates a watcher over the mounted rendered config.
func NewFileWatcher(
	sourcePath, runtimePath, rawKeyPath, keyPath string,
	process Reloader,
	logger *slog.Logger,
) *FileWatcher {
	return &FileWatcher{
		sourcePath:  sourcePath,
		runtimePath: runtimePath,
		rawKeyPath:  rawKeyPath,
		keyPath:     keyPath,
		process:     process,
		testConfig:  NewBinaryConfigTester(PgDoormanBinary),
		logger:      logger,
	}
}

// ApplyInitial materializes the initial config before pg_doorman starts.
// The kubelet does not start the container without the Secret volume, so the
// source file is always present.
func (w *FileWatcher) ApplyInitial(ctx context.Context) error {
	data, err := os.ReadFile(w.sourcePath)
	if err != nil {
		return err
	}
	if err := w.materialize(ctx, data); err != nil {
		return err
	}
	w.lastApplied = data
	return nil
}

// Run polls for config changes until the context is done.
func (w *FileWatcher) Run(ctx context.Context, pollIntervalSec int) {
	ticker := time.NewTicker(time.Duration(pollIntervalSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.check(ctx)
		}
	}
}

func (w *FileWatcher) check(ctx context.Context) {
	data, err := os.ReadFile(w.sourcePath)
	if err != nil {
		w.logger.Warn("failed to read rendered config", "error", err)
		return
	}
	if bytes.Equal(data, w.lastApplied) {
		return
	}

	w.logger.Info("rendered config changed")

	ConfigStale.Set(1)

	newCfg, err := ValidateConfigBytes(data)
	if err != nil {
		w.logger.Error("rendered config is invalid, keeping old config", "error", err)
		ReloadsTotal.WithLabelValues("failure").Inc()
		return
	}

	needsRestart := false
	if oldCfg, parseErr := ValidateConfigBytes(w.lastApplied); parseErr == nil {
		needsRestart = NeedsProcessRestart(oldCfg, newCfg)
	}

	if err := w.materialize(ctx, data); err != nil {
		w.logger.Error("failed to apply rendered config, keeping old config", "error", err)
		ReloadsTotal.WithLabelValues("failure").Inc()
		return
	}

	if needsRestart {
		w.logger.Warn("non-reloadable config fields changed, restarting pg_doorman")
		if err := w.process.Restart(); err != nil {
			w.logger.Error("failed to restart pg_doorman", "error", err)
			ReloadsTotal.WithLabelValues("failure").Inc()
			return
		}
	} else if err := w.process.Reload(); err != nil {
		w.logger.Error("failed to reload pg_doorman", "error", err)
		ReloadsTotal.WithLabelValues("failure").Inc()
		return
	}

	w.lastApplied = data
	ConfigStale.Set(0)
	ReloadsTotal.WithLabelValues("success").Inc()
	w.logger.Info("config reloaded successfully", "restarted", needsRestart)
}

// materialize converts the TLS key, validates the candidate with the real
// binary (SIGHUP delivery does not mean acceptance, and a rejected file on
// disk would crash-loop any later restart) and atomically replaces the
// runtime config.
func (w *FileWatcher) materialize(ctx context.Context, data []byte) error {
	if w.rawKeyPath != "" {
		if err := EnsurePKCS8Key(w.rawKeyPath, w.keyPath); err != nil {
			return err
		}
	}

	candidate := w.runtimePath + CandidateSuffix
	if err := AtomicWrite(candidate, data); err != nil {
		return err
	}
	if w.testConfig != nil {
		if err := w.testConfig(ctx, candidate); err != nil {
			_ = os.Remove(candidate)
			return err
		}
	}
	return os.Rename(candidate, w.runtimePath)
}
