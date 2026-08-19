package wrapper

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeScript(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "fake_doorman.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("file %s did not appear within 5s", path)
}

func TestRunWithRestartStopsGracefullyOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	graceful := filepath.Join(dir, "graceful")
	script := writeScript(t, dir,
		"#!/bin/sh\ntouch "+ready+"\ntrap 'touch "+graceful+"; exit 0' TERM\nwhile :; do sleep 0.05; done\n")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := NewProcess(filepath.Join(dir, "config.yaml"), logger)
	p.binary = script

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.RunWithRestart(ctx) }()

	waitForFile(t, ready)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunWithRestart did not return after context cancel")
	}

	if _, err := os.Stat(graceful); err != nil {
		t.Fatal("process was killed instead of receiving SIGTERM: graceful marker missing")
	}
}

func TestRunWithRestartKillsProcessIgnoringSigterm(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	script := writeScript(t, dir,
		"#!/bin/sh\ntouch "+ready+"\ntrap '' TERM\nwhile :; do sleep 0.05; done\n")

	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("general:\n  shutdown_timeout: 100ms\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := NewProcess(configPath, logger)
	p.binary = script
	p.waitMargin = 200 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.RunWithRestart(ctx) }()

	waitForFile(t, ready)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunWithRestart hung: process ignoring SIGTERM was never killed")
	}
}

func TestWaitDelay(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name   string
		config string
		want   time.Duration
	}{
		{"from config shutdown_timeout", "general:\n  shutdown_timeout: 30s\n", 30*time.Second + 5*time.Second},
		{"config without shutdown_timeout", "general:\n  port: 6432\n", 10*time.Second + 5*time.Second},
		{"invalid duration", "general:\n  shutdown_timeout: nonsense\n", 10*time.Second + 5*time.Second},
		{"missing config file", "", 10*time.Second + 5*time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "config.yaml")
			if tt.config != "" {
				if err := os.WriteFile(configPath, []byte(tt.config), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			p := NewProcess(configPath, logger)

			if got := p.waitDelay(); got != tt.want {
				t.Fatalf("waitDelay() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestBackoffAfterExit(t *testing.T) {
	// Six single crashes spread over weeks must not accumulate to the 30s cap:
	// a long uptime proves the previous start was viable, so the penalty resets.
	if got := backoffAfterExit(maxBackoff, backoffResetUptime+time.Second); got != initialBackoff {
		t.Errorf("long uptime must reset backoff to %v, got %v", initialBackoff, got)
	}
	if got := backoffAfterExit(initialBackoff, time.Second); got != initialBackoff*backoffMultiplier {
		t.Errorf("quick crash must escalate backoff, got %v", got)
	}
	if got := backoffAfterExit(maxBackoff, time.Second); got != maxBackoff {
		t.Errorf("backoff must stay capped at %v, got %v", maxBackoff, got)
	}
}
