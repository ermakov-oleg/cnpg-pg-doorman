//go:build linux

package wrapper

import (
	"log/slog"

	"golang.org/x/sys/unix"
)

// MaxNoFile caps the descriptor limit pg_doorman inherits. Its binary upgrade
// spawns the validator and the successor with PG_DOORMAN_CLOSE_INHERITED_FDS=1,
// and each child then closes every descriptor up to the limit. With the
// container default (~1.07e9 under containerd) that burns ~2 minutes of CPU per
// child, so the successor misses the 10s readiness window and the handover
// always aborts. 64k is far above what max_connections can consume.
const MaxNoFile = 65536

// CapFileDescriptorLimit lowers the soft RLIMIT_NOFILE so that every process
// the wrapper spawns, and every child they spawn in turn, inherits it.
func CapFileDescriptorLimit(logger *slog.Logger) {
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		logger.Warn("failed to read the file descriptor limit", "error", err)
		return
	}
	if limit.Cur <= MaxNoFile {
		return
	}
	capped := unix.Rlimit{Cur: MaxNoFile, Max: limit.Max}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &capped); err != nil {
		logger.Warn("failed to cap the file descriptor limit", "error", err, "current", limit.Cur)
		return
	}
	logger.Info("capped the file descriptor limit", "from", limit.Cur, "to", MaxNoFile)
}
