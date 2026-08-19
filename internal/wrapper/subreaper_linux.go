//go:build linux

package wrapper

import (
	"log/slog"

	"golang.org/x/sys/unix"
)

// SetChildSubreaper makes the wrapper adopt orphaned descendants. As PID 1 in
// the container it already does; this matters for tests and any future
// non-PID-1 deployment, and is required for post-upgrade adoption.
func SetChildSubreaper(logger *slog.Logger) {
	if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
		logger.Warn("failed to set child subreaper", "error", err)
	}
}
