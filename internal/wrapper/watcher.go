package wrapper

import (
	"context"
	"log/slog"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
)

const debounceDelay = 3 * time.Second

// ConfigGenerator generates pg_doorman YAML config from a PgDoorman spec.
type ConfigGenerator func(ctx context.Context, spec *v1alpha1.PgDoormanSpec) ([]byte, error)

// CRDWatcher polls PgDoorman CR for changes and regenerates config.
type CRDWatcher struct {
	client      client.Client
	configName  string
	namespace   string
	runtimePath string
	process     *Process
	logger      *slog.Logger
	generate    ConfigGenerator
	lastGen     int64
}

// NewCRDWatcher creates a new CRD-based watcher.
func NewCRDWatcher(
	cl client.Client,
	configName, namespace string,
	runtimePath string,
	process *Process,
	generate ConfigGenerator,
	logger *slog.Logger,
) *CRDWatcher {
	return &CRDWatcher{
		client:      cl,
		configName:  configName,
		namespace:   namespace,
		runtimePath: runtimePath,
		process:     process,
		generate:    generate,
		logger:      logger,
	}
}

// Run polls the CRD at the given interval.
func (w *CRDWatcher) Run(ctx context.Context, pollIntervalSec int) {
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

func (w *CRDWatcher) check(ctx context.Context) {
	var pgDoorman v1alpha1.PgDoorman
	if err := w.client.Get(ctx, client.ObjectKey{
		Name:      w.configName,
		Namespace: w.namespace,
	}, &pgDoorman); err != nil {
		w.logger.Warn("failed to get PgDoorman CR", "error", err)
		return
	}

	gen := pgDoorman.Generation
	if gen == w.lastGen {
		return
	}

	w.logger.Info("PgDoorman CR changed, debouncing", "oldGen", w.lastGen, "newGen", gen)

	// Debounce: wait to make sure the resource has stabilized
	select {
	case <-ctx.Done():
		return
	case <-time.After(debounceDelay):
	}

	// Re-check generation after debounce
	var pgDoormanAfter v1alpha1.PgDoorman
	if err := w.client.Get(ctx, client.ObjectKey{
		Name:      w.configName,
		Namespace: w.namespace,
	}, &pgDoormanAfter); err != nil {
		w.logger.Warn("failed to re-get PgDoorman CR after debounce", "error", err)
		return
	}
	if pgDoormanAfter.Generation != gen {
		w.logger.Info("PgDoorman CR still changing, will retry on next poll")
		return
	}

	data, err := w.generate(ctx, &pgDoormanAfter.Spec)
	if err != nil {
		w.logger.Error("failed to generate config", "error", err)
		return
	}

	if _, err := ValidateConfigBytes(data); err != nil {
		w.logger.Error("generated config is invalid, keeping old config", "error", err)
		return
	}

	if err := AtomicWrite(w.runtimePath, data); err != nil {
		w.logger.Error("failed to write config", "error", err)
		return
	}

	if err := w.process.Reload(); err != nil {
		w.logger.Error("failed to reload pg_doorman", "error", err)
		return
	}

	w.lastGen = gen
	w.logger.Info("config reloaded successfully", "generation", gen)
}

// WaitForCRDConfig blocks until the PgDoorman CRD generates a valid config.
func WaitForCRDConfig(
	ctx context.Context,
	cl client.Client,
	configName, namespace string,
	runtimePath string,
	generate ConfigGenerator,
	pollSec int,
	logger *slog.Logger,
) {
	ticker := time.NewTicker(time.Duration(pollSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var pgDoorman v1alpha1.PgDoorman
			if err := cl.Get(ctx, client.ObjectKey{Name: configName, Namespace: namespace}, &pgDoorman); err != nil {
				logger.Warn("waiting for PgDoorman CR", "error", err)
				continue
			}

			data, err := generate(ctx, &pgDoorman.Spec)
			if err != nil {
				logger.Warn("config generation failed", "error", err)
				continue
			}

			if _, err := ValidateConfigBytes(data); err != nil {
				logger.Warn("generated config invalid", "error", err)
				continue
			}

			if err := AtomicWrite(runtimePath, data); err != nil {
				logger.Warn("failed to write config", "error", err)
				continue
			}

			logger.Info("initial config generated from CRD")
			return
		}
	}
}
