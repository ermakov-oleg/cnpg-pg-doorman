//go:build !linux

package wrapper

import "log/slog"

// CapFileDescriptorLimit is a no-op outside Linux: the limit only matters for
// the descriptor sweep pg_doorman's upgrade children perform in a container.
func CapFileDescriptorLimit(_ *slog.Logger) {}
