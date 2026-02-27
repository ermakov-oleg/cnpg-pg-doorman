package wrapper

import (
	"context"
	"log/slog"
	"time"
)

const debounceDelay = 3 * time.Second

type Watcher struct {
	configMapPath string
	runtimePath   string
	process       *Process
	logger        *slog.Logger
	lastHash      string
}

func NewWatcher(configMapPath, runtimePath string, process *Process, logger *slog.Logger) *Watcher {
	hash, _ := FileHash(configMapPath)
	return &Watcher{
		configMapPath: configMapPath,
		runtimePath:   runtimePath,
		process:       process,
		logger:        logger,
		lastHash:      hash,
	}
}

func (w *Watcher) Run(ctx context.Context, pollIntervalSec int) {
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

func (w *Watcher) check(ctx context.Context) {
	hash, err := FileHash(w.configMapPath)
	if err != nil {
		w.logger.Warn("failed to hash config file", "error", err)
		return
	}

	if hash == w.lastHash {
		return
	}

	w.logger.Info("config file changed, debouncing", "oldHash", w.lastHash[:8], "newHash", hash[:8])

	// Debounce: ждём чтобы убедиться что файл стабилизировался
	select {
	case <-ctx.Done():
		return
	case <-time.After(debounceDelay):
	}

	// Перепроверяем хеш после debounce
	hashAfter, err := FileHash(w.configMapPath)
	if err != nil {
		w.logger.Warn("failed to re-hash config file after debounce", "error", err)
		return
	}
	if hashAfter != hash {
		w.logger.Info("config still changing, will retry on next poll")
		return
	}

	// Валидируем и применяем
	if err := ValidateAndCopyConfig(w.configMapPath, w.runtimePath, w.logger); err != nil {
		w.logger.Error("new config is invalid, keeping old config", "error", err)
		return
	}

	if err := w.process.Reload(); err != nil {
		w.logger.Error("failed to reload pg_doorman", "error", err)
		return
	}

	w.lastHash = hashAfter
	w.logger.Info("config reloaded successfully")
}

// WaitForValidConfig блокируется пока не появится валидный конфиг.
func WaitForValidConfig(ctx context.Context, src, dst string, pollSec int, logger *slog.Logger) {
	ticker := time.NewTicker(time.Duration(pollSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := ValidateAndCopyConfig(src, dst, logger); err != nil {
				logger.Warn("waiting for valid config", "error", err)
				continue
			}
			return
		}
	}
}
