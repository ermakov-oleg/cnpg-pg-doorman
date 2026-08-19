package wrapper

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"
)

// Restarter gracefully restarts the pg_doorman process.
type Restarter interface {
	Restart() error
}

// RoleWatcher polls the downward-API file with the pod's CNPG instance role
// and gracefully restarts the pooler on demotion (primary -> replica): after
// a failover the demoted primary keeps serving long-lived client connections
// that would otherwise hit read-only errors forever; the drain forces clients
// to reconnect through the Service to the new primary.
type RoleWatcher struct {
	roleFile string
	process  Restarter
	logger   *slog.Logger
	lastRole string
}

// NewRoleWatcher creates a RoleWatcher over the given downward-API role file.
func NewRoleWatcher(roleFile string, process Restarter, logger *slog.Logger) *RoleWatcher {
	return &RoleWatcher{
		roleFile: roleFile,
		process:  process,
		logger:   logger,
	}
}

// Run polls the role file at the given interval until the context is done.
func (w *RoleWatcher) Run(ctx context.Context, pollIntervalSec int) {
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

func (w *RoleWatcher) check(_ context.Context) {
	data, err := os.ReadFile(w.roleFile)
	if err != nil {
		// The downward-API file may not exist yet (label unset on a fresh pod).
		return
	}
	role := strings.TrimSpace(string(data))
	if role == "" || role == w.lastRole {
		return
	}

	demoted := w.lastRole == "primary" && role != "primary"
	w.lastRole = role
	if !demoted {
		return
	}

	w.logger.Warn("instance demoted, restarting pg_doorman to drop client sessions",
		"newRole", role)
	if err := w.process.Restart(); err != nil {
		w.logger.Error("failed to restart pg_doorman after demotion", "error", err)
		// Retry on the next poll: reset so the transition is seen again.
		w.lastRole = "primary"
	}
}
