package wrapper

import (
	"context"
	"log/slog"
	"os"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
)

// ConfigGenerator generates pg_doorman YAML config from a PgDoorman spec.
type ConfigGenerator func(ctx context.Context, spec *v1alpha1.PgDoormanSpec) ([]byte, error)

// SecretHashFunc computes a hash over Secrets referenced by the spec.
// Returns empty string if no secrets are referenced.
type SecretHashFunc func(ctx context.Context, spec *v1alpha1.PgDoormanSpec, namespace string) (string, error)

// Reloader asks the running pg_doorman process to re-read its config, or to
// restart when the change touches fields SIGHUP cannot apply.
type Reloader interface {
	Reload() error
	Restart() error
}

// CRDWatcher polls PgDoorman CR for changes and regenerates config.
type CRDWatcher struct {
	client         client.Client
	configName     string
	namespace      string
	runtimePath    string
	process        Reloader
	logger         *slog.Logger
	generate       ConfigGenerator
	secretHash     SecretHashFunc
	testConfig     ConfigTester
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
	process Reloader,
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
		testConfig:     NewBinaryConfigTester(PgDoormanBinary),
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

	newCfg, err := ValidateConfigBytes(data)
	if err != nil {
		w.logger.Error("generated config is invalid, keeping old config", "error", err)
		return
	}

	// Capture the currently applied config before the file is replaced, to
	// detect changes in fields SIGHUP cannot apply.
	needsRestart := false
	if oldData, readErr := os.ReadFile(w.runtimePath); readErr == nil {
		if oldCfg, parseErr := ValidateConfigBytes(oldData); parseErr == nil {
			needsRestart = NeedsProcessRestart(oldCfg, newCfg)
		}
	}

	// Validate the candidate with the real pg_doorman binary before it ever
	// replaces the runtime file: SIGHUP delivery does not mean the config was
	// accepted, and a rejected file left on disk would crash-loop the process
	// on any later restart.
	candidate := w.runtimePath + CandidateSuffix
	if err := AtomicWrite(candidate, data); err != nil {
		w.logger.Error("failed to write candidate config", "error", err)
		return
	}
	if w.testConfig != nil {
		if err := w.testConfig(ctx, candidate); err != nil {
			w.logger.Error("pg_doorman rejected generated config, keeping old config", "error", err)
			_ = os.Remove(candidate)
			return
		}
	}
	if err := os.Rename(candidate, w.runtimePath); err != nil {
		w.logger.Error("failed to replace config", "error", err)
		return
	}

	if needsRestart {
		w.logger.Warn("non-reloadable config fields changed, restarting pg_doorman", "generation", gen)
		if err := w.process.Restart(); err != nil {
			w.logger.Error("failed to restart pg_doorman", "error", err)
			return
		}
	} else if err := w.process.Reload(); err != nil {
		w.logger.Error("failed to reload pg_doorman", "error", err)
		return
	}

	w.lastGen = gen
	if secretHashOK {
		w.lastSecretHash = secretHash
	}
	w.logger.Info("config reloaded successfully", "generation", gen, "restarted", needsRestart)
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
	testConfig ConfigTester,
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

			candidate := runtimePath + CandidateSuffix
			if err := AtomicWrite(candidate, data); err != nil {
				logger.Warn("failed to write candidate config", "error", err)
				continue
			}
			if testConfig != nil {
				if err := testConfig(ctx, candidate); err != nil {
					logger.Warn("pg_doorman rejected generated config", "error", err)
					_ = os.Remove(candidate)
					continue
				}
			}
			if err := os.Rename(candidate, runtimePath); err != nil {
				logger.Warn("failed to replace config", "error", err)
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
