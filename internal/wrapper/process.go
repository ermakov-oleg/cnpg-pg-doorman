package wrapper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"log/slog"

	"gopkg.in/yaml.v3"
)

const (
	// PgDoormanBinary is the runtime pg_doorman path. It lives on tmpfs (the
	// image copy is seeded/synced by BinarySyncer): the upstream binary
	// upgrade re-executes argv[0], so the path must be replaceable.
	PgDoormanBinary   = RuntimeBinaryPath
	initialBackoff    = 1 * time.Second
	maxBackoff        = 30 * time.Second
	backoffMultiplier = 2
	// backoffResetUptime: a process that lived this long proves the previous
	// start was viable — reset the penalty instead of accumulating it across
	// rare crashes spread over weeks.
	backoffResetUptime     = 60 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	shutdownWaitMargin     = 5 * time.Second
	// successorSearchTimeout bounds the search for the re-exec'd pg_doorman: it
	// only becomes visible as our child once the old process exits.
	successorSearchTimeout = 5 * time.Second
	successorPollInterval  = 200 * time.Millisecond
	// adoptedLivenessInterval paces the kill(pid, 0) fallback used when wait4
	// cannot report the adopted process (ECHILD).
	adoptedLivenessInterval = 200 * time.Millisecond
)

// errAdoptedNotChild reports that the adopted process is gone but its exit
// status was unavailable: wait4 returned ECHILD, so it was never (or no longer)
// our child and only its disappearance could be observed.
var errAdoptedNotChild = errors.New("adopted pg_doorman is not our child")

type Process struct {
	configPath string
	binary     string
	waitMargin time.Duration
	cmd        *exec.Cmd
	// adoptedPid is the pid of a post-upgrade successor that re-executed itself
	// and reparented to the wrapper: it is supervised by pid instead of by cmd.
	adoptedPid int
	// upgradeRequested marks that the next process exit is (probably) the old
	// binary handing over to its re-exec'd successor, not a crash.
	upgradeRequested bool
	successorTimeout time.Duration
	mu               sync.Mutex
	logger           *slog.Logger
}

func NewProcess(configPath string, logger *slog.Logger) *Process {
	return &Process{
		configPath:       configPath,
		binary:           PgDoormanBinary,
		waitMargin:       shutdownWaitMargin,
		successorTimeout: successorSearchTimeout,
		logger:           logger,
	}
}

// waitDelay bounds how long to wait for pg_doorman to exit after SIGTERM:
// shutdown_timeout from the config (or the default) plus a margin, then SIGKILL.
func (p *Process) waitDelay() time.Duration {
	timeout := defaultShutdownTimeout
	if data, err := os.ReadFile(p.configPath); err == nil {
		var cfg DoormanConfig
		if yaml.Unmarshal(data, &cfg) == nil {
			if d, err := time.ParseDuration(cfg.General.ShutdownTimeout); err == nil {
				timeout = d
			}
		}
	}
	return timeout + p.waitMargin
}

func (p *Process) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	cmd := exec.CommandContext(ctx, p.binary, p.configPath) //nolint:gosec // fixed binary path, config path is our constant
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// The default Cancel sends SIGKILL — pg_doorman gets no chance to close client connections.
	cmd.Cancel = func() error {
		p.logger.Info("sending SIGTERM to pg_doorman", "pid", cmd.Process.Pid)
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = p.waitDelay()
	p.cmd = cmd
	p.adoptedPid = 0

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start pg_doorman: %w", err)
	}

	p.logger.Info("pg_doorman started", "pid", p.cmd.Process.Pid)
	return nil
}

// Pid returns the currently supervised pid (0 when not running).
func (p *Process) Pid() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pidLocked()
}

func (p *Process) pidLocked() int {
	if p.adoptedPid != 0 {
		return p.adoptedPid
	}
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

// signalLocked sends sig to the supervised process, either the direct child or
// the adopted post-upgrade one. Callers hold p.mu.
func (p *Process) signalLocked(sig syscall.Signal) error {
	pid := p.pidLocked()
	if pid == 0 {
		return fmt.Errorf("process not running")
	}
	return syscall.Kill(pid, sig)
}

func (p *Process) Wait() error {
	p.mu.Lock()
	cmd := p.cmd
	adopted := p.adoptedPid
	p.mu.Unlock()

	if adopted != 0 {
		err := waitPid(adopted)
		// Forget the pid we just reaped: a SIGKILL timer armed by
		// terminateIfAdopted must not fire at whoever reuses that pid next.
		p.mu.Lock()
		if p.adoptedPid == adopted {
			p.adoptedPid = 0
		}
		p.mu.Unlock()
		return err
	}
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("process not started")
	}
	return cmd.Wait()
}

// waitPid blocks until the adopted process exits. Only the adopted successor is
// waited this way: a global wait4(-1) reaper would steal exit statuses from the
// exec.Cmd users, such as the config validator.
func waitPid(pid int) error {
	var ws syscall.WaitStatus
	for {
		_, err := syscall.Wait4(pid, &ws, 0, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.ECHILD) {
			// Not our child: reporting an exit now would restart pg_doorman
			// while it is still serving. Wait for it to actually disappear.
			return waitUntilGone(pid)
		}
		if err != nil {
			return fmt.Errorf("wait4 pid %d: %w", pid, err)
		}
		break
	}
	if ws.ExitStatus() != 0 || ws.Signaled() {
		return fmt.Errorf("adopted pg_doorman exited: status %d signaled %v", ws.ExitStatus(), ws.Signaled())
	}
	return nil
}

// waitUntilGone polls liveness until the process no longer exists. Cancellation
// is handled by the caller's terminateIfAdopted, which kills pid and thereby
// ends this loop.
func waitUntilGone(pid int) error {
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("%w: pid %d disappeared", errAdoptedNotChild, pid)
		}
		time.Sleep(adoptedLivenessInterval)
	}
}

// terminateIfAdopted mirrors the cmd.Cancel+WaitDelay sequence for the adopted
// process, which has no exec.Cmd to propagate context cancellation: SIGTERM,
// then SIGKILL after waitDelay.
func (p *Process) terminateIfAdopted() {
	p.mu.Lock()
	pid := p.adoptedPid
	p.mu.Unlock()
	if pid == 0 {
		return
	}

	p.logger.Info("sending SIGTERM to adopted pg_doorman", "pid", pid)
	p.killAdopted(pid, syscall.SIGTERM)
	time.AfterFunc(p.waitDelay(), func() {
		p.killAdopted(pid, syscall.SIGKILL)
	})
}

// killAdopted signals pid unless it is no longer the supervised one; a process
// that already exited (ESRCH) is not an error.
func (p *Process) killAdopted(pid int, sig syscall.Signal) {
	p.mu.Lock()
	current := p.adoptedPid
	p.mu.Unlock()
	if current != pid {
		return
	}
	if err := syscall.Kill(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		p.logger.Warn("failed to signal adopted pg_doorman", "pid", pid, "signal", sig, "error", err)
	}
}

// Restart gracefully stops pg_doorman (SIGTERM honors shutdown_timeout);
// RunWithRestart then starts a new process, which picks up the new config.
// Used for non-reloadable config fields that SIGHUP silently ignores.
func (p *Process) Restart() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	pid := p.pidLocked()
	if pid == 0 {
		return fmt.Errorf("process not running")
	}

	p.logger.Info("sending SIGTERM to pg_doorman to apply non-reloadable config", "pid", pid)
	return p.signalLocked(syscall.SIGTERM)
}

// Upgrade triggers the upstream zero-downtime binary upgrade: pg_doorman
// validates argv[0], spawns the successor with the inherited listener,
// migrates idle clients and exits. RunWithRestart adopts the successor.
func (p *Process) Upgrade() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.signalLocked(syscall.SIGUSR2); err != nil {
		return err
	}
	p.upgradeRequested = true
	p.logger.Info("sent SIGUSR2 to pg_doorman to start binary upgrade", "pid", p.pidLocked())
	return nil
}

func (p *Process) consumeUpgradeRequest() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	requested := p.upgradeRequested
	p.upgradeRequested = false
	return requested
}

// adoptSuccessor looks for the re-exec'd pg_doorman for a few seconds after
// the old process exited (the successor reparents to us at that moment).
func (p *Process) adoptSuccessor(oldPid int) bool {
	deadline := time.Now().Add(p.successorTimeout)
	for {
		if pid, ok := findSuccessorPid(p.binary, oldPid); ok {
			p.mu.Lock()
			p.cmd = nil
			p.adoptedPid = pid
			p.mu.Unlock()
			p.logger.Info("binary upgrade completed, adopted new pg_doorman", "pid", pid, "oldPid", oldPid)
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(successorPollInterval)
	}
}

func (p *Process) Reload() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	pid := p.pidLocked()
	if pid == 0 {
		return fmt.Errorf("process not running")
	}

	p.logger.Info("sending SIGHUP to pg_doorman", "pid", pid)
	return p.signalLocked(syscall.SIGHUP)
}

// backoffAfterExit returns the delay before the next start attempt.
func backoffAfterExit(current, uptime time.Duration) time.Duration {
	if uptime >= backoffResetUptime {
		return initialBackoff
	}
	return min(current*backoffMultiplier, maxBackoff)
}

// RunWithRestart runs pg_doorman and restarts it on crash with exponential backoff.
// The backoff resets after a sufficiently long uptime. A binary upgrade handover
// (SIGUSR2) is not a crash: the successor is adopted and supervised in place.
// Stops when the context is canceled.
func (p *Process) RunWithRestart(ctx context.Context) error {
	backoff := initialBackoff

	for {
		startedAt := time.Now()
		if err := p.Start(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			p.logger.Error("failed to start pg_doorman, retrying", "error", err, "backoff", backoff)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(backoff*backoffMultiplier, maxBackoff)
			continue
		}
		pid := p.Pid()

		// Supervise the current process and every successor it hands over to;
		// only a real exit leaves this loop for the backoff/Start path.
		for {
			// exec.CommandContext handles cancellation for a direct child; the
			// adopted process needs the SIGTERM/SIGKILL sequence arranged here.
			stopTerm := context.AfterFunc(ctx, p.terminateIfAdopted)
			err := p.Wait()
			stopTerm()

			if ctx.Err() != nil {
				return ctx.Err()
			}

			if p.consumeUpgradeRequest() {
				if p.adoptSuccessor(pid) {
					BinaryUpgradesTotal.WithLabelValues("success").Inc()
					// The handover is not a restart: uptime accounting starts
					// over for the adopted process, the backoff is untouched.
					startedAt = time.Now()
					pid = p.Pid()
					continue
				}
				BinaryUpgradesTotal.WithLabelValues("failure").Inc()
				p.logger.Error("binary upgrade handover failed: successor not found, restarting pg_doorman")
			}

			uptime := time.Since(startedAt)
			ProcessRestartsTotal.Inc()
			backoff = backoffAfterExit(backoff, uptime)
			p.logger.Error("pg_doorman exited unexpectedly, restarting", "error", err, "backoff", backoff, "uptime", uptime)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			break
		}
	}
}
