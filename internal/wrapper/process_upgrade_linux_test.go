//go:build linux

package wrapper

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fake pg_doorman: on USR2 it spawns a detached copy of itself (the
// "successor"), then exits 0 — mirroring the upstream binary upgrade flow.
const upgradeScript = `#!/bin/sh
echo $$ >> "$PID_LOG"
trap 'exit 0' TERM
trap '"$0" "$@" & exit 0' USR2
while :; do sleep 0.1; done
`

// noSuccessorScript exits on USR2 without spawning anything: the handover
// cannot complete and the supervisor must fall back to a restart.
const noSuccessorScript = `#!/bin/sh
echo $$ >> "$PID_LOG"
trap 'exit 0' TERM USR2
while :; do sleep 0.1; done
`

func TestUpgradeAdoptsSuccessor(t *testing.T) {
	SetChildSubreaper(slog.Default())
	p, pidLog := newUpgradeProcess(t, upgradeScript)
	before := testutil.ToFloat64(BinaryUpgradesTotal.WithLabelValues("success"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.RunWithRestart(ctx) }()

	waitForPidCount(t, pidLog, 1)
	firstPid := p.Pid()

	if err := p.Upgrade(); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}

	// The successor writes its pid, the old process exits, adoption follows.
	waitForPidCount(t, pidLog, 2)
	adopted := waitForNewPid(t, p, firstPid)
	cancel()
	<-done

	if got := testutil.ToFloat64(BinaryUpgradesTotal.WithLabelValues("success")) - before; got != 1 {
		t.Errorf("success upgrades counter delta = %v, want 1", got)
	}
	if pids := readPids(t, pidLog); pids[1] != adopted {
		t.Errorf("adopted pid %d is not the successor %d", adopted, pids[1])
	}
}

// The adopted process is supervised through waitPid, not exec.Cmd: its death
// must be noticed and must produce a normal restart.
func TestUpgradeAdoptedProcessExitTriggersRestart(t *testing.T) {
	SetChildSubreaper(slog.Default())
	p, pidLog := newUpgradeProcess(t, upgradeScript)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.RunWithRestart(ctx) }()

	waitForPidCount(t, pidLog, 1)
	firstPid := p.Pid()
	if err := p.Upgrade(); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	waitForPidCount(t, pidLog, 2)
	adopted := waitForNewPid(t, p, firstPid)

	restarts := testutil.ToFloat64(ProcessRestartsTotal)
	if err := syscall.Kill(adopted, syscall.SIGKILL); err != nil {
		t.Fatalf("kill adopted pid %d: %v", adopted, err)
	}

	// A third pid in the log means the supervisor observed the adopted exit.
	waitForPidCount(t, pidLog, 3)
	cancel()
	<-done

	if got := testutil.ToFloat64(ProcessRestartsTotal) - restarts; got != 1 {
		t.Errorf("restart counter delta = %v, want 1", got)
	}
}

func TestUpgradeWithoutSuccessorFallsBackToRestart(t *testing.T) {
	SetChildSubreaper(slog.Default())
	p, pidLog := newUpgradeProcess(t, noSuccessorScript)
	// No successor will ever appear: do not burn the full search window.
	p.successorTimeout = 300 * time.Millisecond
	before := testutil.ToFloat64(BinaryUpgradesTotal.WithLabelValues("failure"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.RunWithRestart(ctx) }()

	waitForPidCount(t, pidLog, 1)
	if err := p.Upgrade(); err != nil {
		t.Fatalf("upgrade failed: %v", err)
	}
	// Fallback: a fresh direct child appears (second pid in the log).
	waitForPidCount(t, pidLog, 2)
	cancel()
	<-done

	if got := testutil.ToFloat64(BinaryUpgradesTotal.WithLabelValues("failure")) - before; got != 1 {
		t.Errorf("failure upgrades counter delta = %v, want 1", got)
	}
}

// waitPid falls back to liveness polling when wait4 cannot report the process
// (ECHILD): a still-running pooler must never be reported as exited.
func TestWaitUntilGoneReturnsOnlyAfterProcessDisappears(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "while :; do sleep 0.05; done")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid

	done := make(chan error, 1)
	go func() { done <- waitUntilGone(pid) }()

	select {
	case err := <-done:
		t.Fatalf("returned while the process was alive: %v", err)
	case <-time.After(500 * time.Millisecond):
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	// Reap it: a zombie still answers to kill(pid, 0).
	_ = cmd.Wait()

	select {
	case err := <-done:
		if !errors.Is(err, errAdoptedNotChild) {
			t.Fatalf("error = %v, want errAdoptedNotChild", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitUntilGone did not return after the process disappeared")
	}
}

func TestFindSuccessorPidSkipsExcludedAndSelf(t *testing.T) {
	self, err := os.Readlink("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	if pid, ok := findSuccessorPid(self, 0); ok {
		t.Fatalf("found pid %d running the test binary itself", pid)
	}

	dir := t.TempDir()
	script := filepath.Join(dir, "pg_doorman")
	if err := os.WriteFile(script, []byte(noSuccessorScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PID_LOG", filepath.Join(dir, "pids"))

	p := NewProcess(filepath.Join(dir, "config.yaml"), testLogger())
	p.binary = script
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	pid := p.Pid()
	defer func() {
		cancel()
		_ = p.Wait()
	}()

	waitForPidCount(t, filepath.Join(dir, "pids"), 1)
	if found, ok := findSuccessorPid(script, 0); !ok || found != pid {
		t.Fatalf("findSuccessorPid = (%d, %v), want (%d, true)", found, ok, pid)
	}
	if found, ok := findSuccessorPid(script, pid); ok {
		t.Fatalf("excluded pid was returned: %d", found)
	}
}

func newUpgradeProcess(t *testing.T, body string) (*Process, string) {
	t.Helper()
	dir := t.TempDir()
	pidLog := filepath.Join(dir, "pids")
	script := filepath.Join(dir, "pg_doorman")
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PID_LOG", pidLog)

	p := NewProcess(filepath.Join(dir, "config.yaml"), testLogger())
	p.binary = script
	return p, pidLog
}

// waitForNewPid waits until the supervised pid differs from prev and is set.
func waitForNewPid(t *testing.T, p *Process, prev int) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if pid := p.Pid(); pid != 0 && pid != prev {
			return pid
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("supervised pid never changed from %d", prev)
	return 0
}

func readPids(t *testing.T, pidLog string) []int {
	t.Helper()
	data, err := os.ReadFile(pidLog)
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, line := range splitLines(data) {
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			t.Fatalf("bad pid line %q: %v", line, err)
		}
		pids = append(pids, pid)
	}
	return pids
}

func waitForPidCount(t *testing.T, pidLog string, want int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(pidLog)
		count := 0
		for _, line := range splitLines(data) {
			if line != "" {
				count++
			}
		}
		if count >= want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("expected %d pids in %s", want, pidLog)
}

func splitLines(data []byte) []string {
	var out []string
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, string(data[start:i]))
			start = i + 1
		}
	}
	return append(out, string(data[start:]))
}
