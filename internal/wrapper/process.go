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
)

const (
	pgDoormanBinary   = "/usr/local/bin/pg_doorman"
	initialBackoff    = 1 * time.Second
	maxBackoff        = 30 * time.Second
	backoffMultiplier = 2
)

type Process struct {
	configPath string
	cmd        *exec.Cmd
	mu         sync.Mutex
	logger     *slog.Logger
}

func NewProcess(configPath string, logger *slog.Logger) *Process {
	return &Process{
		configPath: configPath,
		logger:     logger,
	}
}

func (p *Process) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.cmd = exec.CommandContext(ctx, pgDoormanBinary, p.configPath)
	p.cmd.Stdout = os.Stdout
	p.cmd.Stderr = os.Stderr

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

func (p *Process) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}

	p.logger.Info("sending SIGTERM to pg_doorman", "pid", p.cmd.Process.Pid)
	return p.cmd.Process.Signal(syscall.SIGTERM)
}

// RunWithRestart запускает pg_doorman и перезапускает при crash с exponential backoff.
// Останавливается при отмене контекста.
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
