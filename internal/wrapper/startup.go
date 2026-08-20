package wrapper

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ApplyInitialConfig materializes the rendered config before pg_doorman starts,
// falling back to the image binary when the installed one rejects it. A rejected
// config is not necessarily broken: startup may have installed a binary whose
// config schema differs from the one the controller rendered (e.g. delivery was
// unreachable and an older image binary was used), and serving on the image
// binary beats crash-looping the pod.
//
// seed is the BinaryWatcher seed from EnsureAtStartup; the returned seed is nil
// after a revert, so the watcher re-applies the desired spec through its own
// download-validate-swap path instead of leaving the pod on the image binary.
func ApplyInitialConfig(
	ctx context.Context,
	fw *FileWatcher,
	syncer *BinarySyncer,
	seed []byte,
	logger *slog.Logger,
) ([]byte, error) {
	err := fw.ApplyInitial(ctx)
	if err == nil {
		return seed, nil
	}

	reverted, revertErr := syncer.RevertToImage()
	if revertErr != nil {
		return nil, errors.Join(err, fmt.Errorf("reverting to the image binary: %w", revertErr))
	}
	if !reverted {
		return nil, err
	}

	logger.Error("installed binary rejected the rendered config, retrying with the image binary", "error", err)
	BinaryStale.Set(1)
	if err := fw.ApplyInitial(ctx); err != nil {
		return nil, err
	}
	return nil, nil
}
