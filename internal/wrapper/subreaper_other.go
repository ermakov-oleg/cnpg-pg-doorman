//go:build !linux

package wrapper

import "log/slog"

// SetChildSubreaper is a no-op outside Linux: PR_SET_CHILD_SUBREAPER has no
// equivalent, and the wrapper only ever runs as PID 1 in a Linux container.
func SetChildSubreaper(_ *slog.Logger) {}
