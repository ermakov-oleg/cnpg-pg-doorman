package wrapper

import (
	"context"
	"log/slog"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
)

// ConfigGenerator generates pg_doorman YAML config from a PgDoorman spec.
type ConfigGenerator func(ctx context.Context, spec *v1alpha1.PgDoormanSpec) ([]byte, error)

// SecretHashFunc computes a hash over Secrets referenced by the spec.
// Returns empty string if no secrets are referenced.
type SecretHashFunc func(ctx context.Context, spec *v1alpha1.PgDoormanSpec, namespace string) (string, error)

// CRDWatcher polls PgDoorman CR for changes and regenerates config.
type CRDWatcher struct {
	client         client.Client
	configName     string
	namespace      string
	runtimePath    string
	process        *Process
	logger         *slog.Logger
	generate       ConfigGenerator
	secretHash     SecretHashFunc
	lastGen        int64
	lastSecretHash string
}

// NewCRDWatcher creates a new CRD-based watcher.
// initialGeneration should be set to the CR generation after the initial config was written
// to avoid a spurious reload on first poll.
func NewCRDWatcher(
	cl client.Client,
	configName, namespace string,
	runtimePath string,
	process *Process,
	generate ConfigGenerator,
	secretHashFn SecretHashFunc,
	logger *slog.Logger,
	initialGeneration int64,
	initialSecretHash string,
) *CRDWatcher {
	return &CRDWatcher{
		client:         cl,
		configName:     configName,
		namespace:      namespace,
		runtimePath:    runtimePath,
		process:        process,
		generate:       generate,
		secretHash:     secretHashFn,
		logger:         logger,
		lastGen:        initialGeneration,
		lastSecretHash: initialSecretHash,
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

	var secretHash string
	secretHashOK := true
	if w.secretHash != nil {
		var shErr error
		secretHash, shErr = w.secretHash(ctx, &pgDoorman.Spec, w.namespace)
		if shErr != nil {
			w.logger.Warn("failed to collect secret versions", "error", shErr)
			secretHashOK = false
		}
	}

	genChanged := gen != w.lastGen
	secretChanged := secretHashOK && secretHash != w.lastSecretHash

	if !genChanged && !secretChanged {
		return
	}

	w.logger.Info("PgDoorman config changed", "oldGen", w.lastGen, "newGen", gen,
		"secretHashChanged", secretChanged)

	data, err := w.generate(ctx, &pgDoorman.Spec)
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
	if secretHashOK {
		w.lastSecretHash = secretHash
	}
	w.logger.Info("config reloaded successfully", "generation", gen)
}

// InitialConfig holds the initial generation and secret hash from WaitForCRDConfig.
type InitialConfig struct {
	Generation int64
	SecretHash string
}

// WaitForCRDConfig blocks until the PgDoorman CRD generates a valid config.
// Returns the CR generation and secret hash that produced the config.
func WaitForCRDConfig(
	ctx context.Context,
	cl client.Client,
	configName, namespace string,
	runtimePath string,
	generate ConfigGenerator,
	secretHashFn SecretHashFunc,
	pollSec int,
	logger *slog.Logger,
) InitialConfig {
	ticker := time.NewTicker(time.Duration(pollSec) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return InitialConfig{}
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

			var secretHash string
			if secretHashFn != nil {
				secretHash, _ = secretHashFn(ctx, &pgDoorman.Spec, namespace)
			}

			logger.Info("initial config generated from CRD")
			return InitialConfig{
				Generation: pgDoorman.Generation,
				SecretHash: secretHash,
			}
		}
	}
}
