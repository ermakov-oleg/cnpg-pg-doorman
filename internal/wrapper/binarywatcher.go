package wrapper

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"runtime"
	"time"
)

// Upgrader triggers the in-place pg_doorman binary upgrade.
type Upgrader interface {
	Upgrade() error
}

// binaryCandidateSuffix keeps the not-yet-validated download away from the
// live binary path.
const binaryCandidateSuffix = ".next"

// BinaryWatcher polls the mounted binary.json and performs the in-place
// upgrade: download, digest check, config validation with the new binary,
// atomic swap of argv[0], then the handover signal.
type BinaryWatcher struct {
	specPath    string
	runtimePath string
	configPath  string
	arch        string
	syncer      *BinarySyncer
	process     Upgrader
	testConfig  ConfigTester
	logger      *slog.Logger
	lastSpec    []byte
}

func NewBinaryWatcher(specPath string, syncer *BinarySyncer, process Upgrader, logger *slog.Logger) *BinaryWatcher {
	return &BinaryWatcher{
		specPath:    specPath,
		runtimePath: RuntimeBinaryPath,
		configPath:  RuntimeConfigPath,
		arch:        runtime.GOARCH,
		syncer:      syncer,
		process:     process,
		logger:      logger,
	}
}

// Seed records the spec applied at startup so an unchanged file does not
// retrigger the flow.
func (w *BinaryWatcher) Seed(lastSpec []byte) { w.lastSpec = lastSpec }

// Run polls for binary spec changes until the context is done.
func (w *BinaryWatcher) Run(ctx context.Context, pollIntervalSec int) {
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

func (w *BinaryWatcher) check(ctx context.Context) {
	data, err := os.ReadFile(w.specPath)
	if err != nil {
		// Absent key: binary delivery is disabled or the plugin predates it.
		return
	}
	if bytes.Equal(data, w.lastSpec) {
		return
	}
	w.logger.Info("binary spec changed")

	spec, err := ParseBinarySpec(data)
	if err != nil {
		// lastSpec is left alone: the next poll retries, so a spec fixed by the
		// controller is picked up without another change of the file.
		w.logger.Error("invalid binary spec, keeping current binary", "error", err)
		return
	}
	desired := spec.SHA256[w.arch]
	if desired == "" {
		w.logger.Warn("binary spec has no digest for this arch", "arch", w.arch)
		w.lastSpec = data
		return
	}
	if cur, err := FileSHA256(w.runtimePath); err == nil && cur == desired {
		w.lastSpec = data
		BinaryStale.Set(0)
		return
	}

	BinaryStale.Set(1)
	candidate := w.runtimePath + binaryCandidateSuffix
	if err := w.syncer.Download(ctx, spec, desired, candidate); err != nil {
		w.logger.Error("binary download failed, will retry", "error", err)
		return
	}

	tester := w.testConfig
	if tester == nil {
		tester = NewBinaryConfigTester(candidate)
	}
	if err := tester(ctx, w.configPath); err != nil {
		w.logger.Error("new binary rejected the current config, keeping current binary", "error", err)
		_ = os.Remove(candidate)
		return
	}

	// The swap must precede the handover signal: the upstream upgrade validates
	// and re-executes argv[0], which has to be the new binary already.
	if err := os.Rename(candidate, w.runtimePath); err != nil {
		w.logger.Error("failed to install new binary", "error", err)
		return
	}
	if err := w.process.Upgrade(); err != nil {
		w.logger.Error("failed to trigger binary upgrade", "error", err)
		return
	}
	w.lastSpec = data
	BinaryStale.Set(0)
	w.logger.Info("in-place binary upgrade triggered", "sha256", desired)
}
