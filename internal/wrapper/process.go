package wrapper

import (
	"context"
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
	// PgDoormanBinary is the pg_doorman path inside the sidecar image.
	PgDoormanBinary        = "/usr/bin/pg_doorman"
	initialBackoff         = 1 * time.Second
	maxBackoff             = 30 * time.Second
	backoffMultiplier      = 2
	defaultShutdownTimeout = 10 * time.Second
	shutdownWaitMargin     = 5 * time.Second
)

type Process struct {
	configPath string
	binary     string
	waitMargin time.Duration
	cmd        *exec.Cmd
	mu         sync.Mutex
	logger     *slog.Logger
}

func NewProcess(configPath string, logger *slog.Logger) *Process {
	return &Process{
		configPath: configPath,
		binary:     PgDoormanBinary,
		waitMargin: shutdownWaitMargin,
		logger:     logger,
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

	cmd := exec.CommandContext(ctx, p.binary, p.configPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// The default Cancel sends SIGKILL — pg_doorman gets no chance to close client connections.
	cmd.Cancel = func() error {
		p.logger.Info("sending SIGTERM to pg_doorman", "pid", cmd.Process.Pid)
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = p.waitDelay()
	p.cmd = cmd

	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start pg_doorman: %w", err)
	}

	p.logger.Info("pg_doorman started", "pid", p.cmd.Process.Pid)
	return nil
}

func (p *Process) Wait() error {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()

	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("process not started")
	}
	return cmd.Wait()
}

func (p *Process) Reload() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil || p.cmd.Process == nil {
		return fmt.Errorf("process not running")
	}

	p.logger.Info("sending SIGHUP to pg_doorman", "pid", p.cmd.Process.Pid)
	return p.cmd.Process.Signal(syscall.SIGHUP)
}

// RunWithRestart runs pg_doorman and restarts it on crash with exponential backoff.
// Stops when the context is canceled.
func (p *Process) RunWithRestart(ctx context.Context) error {
	backoff := initialBackoff

	for {
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

		err := p.Wait()
		if ctx.Err() != nil {
			return ctx.Err()
		}

		p.logger.Error("pg_doorman exited unexpectedly, restarting", "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*backoffMultiplier, maxBackoff)
	}
}
