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
// The readiness flag mirrors the upstream handover: the old process exits only
// after the successor reported itself ready, so the supervisor never adopts a
// process that has not installed its signal handlers yet.
const upgradeScript = `#!/bin/sh
trap 'exit 0' TERM
trap 'echo $$ >> "$HUP_LOG"' HUP
trap 'rm -f "$READY_FLAG"; "$0" "$@" & while [ ! -f "$READY_FLAG" ]; do sleep 0.05; done; exit 0' USR2
echo $$ >> "$PID_LOG"
touch "$READY_FLAG"
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
	// The successor inherited a config snapshot: the supervisor resyncs it.
	waitForPidCount(t, hupLogFor(pidLog), 1)
	hups := readPids(t, hupLogFor(pidLog))
	cancel()
	<-done

	if got := testutil.ToFloat64(BinaryUpgradesTotal.WithLabelValues("success")) - before; got != 1 {
		t.Errorf("success upgrades counter delta = %v, want 1", got)
	}
	if pids := readPids(t, pidLog); pids[1] != adopted {
		t.Errorf("adopted pid %d is not the successor %d", adopted, pids[1])
	}
	if hups[0] != adopted {
		t.Errorf("post-handover SIGHUP went to pid %d, want the adopted %d", hups[0], adopted)
	}
}

// An upgrade pg_doorman aborted itself (rejected config, successor never became
// ready) leaves the old process running: a later, unrelated exit must restart
// normally instead of spending the successor search on a handover that never was.
func TestStaleUpgradeRequestIsNotTreatedAsHandover(t *testing.T) {
	p := NewProcess(filepath.Join(t.TempDir(), "config.yaml"), testLogger())

	p.mu.Lock()
	p.upgradeRequested = true
	p.upgradeRequestedAt = time.Now().Add(-time.Hour)
	p.mu.Unlock()
	if p.consumeUpgradeRequest() {
		t.Error("stale upgrade request must be ignored")
	}

	p.mu.Lock()
	p.upgradeRequested = true
	p.upgradeRequestedAt = time.Now()
	p.mu.Unlock()
	if !p.consumeUpgradeRequest() {
		t.Error("fresh upgrade request must be honored")
	}
	if p.consumeUpgradeRequest() {
		t.Error("upgrade request must be consumed exactly once")
	}
}

// Config validation runs the same binary; adopting such a child would leave the
// supervisor watching a process that exits within seconds.
func TestFindSuccessorPidSkipsConfigValidation(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "pg_doorman")
	if err := os.WriteFile(script, []byte(noSuccessorScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PID_LOG", filepath.Join(dir, "pids"))

	for _, flag := range []string{ValidateConfigFlag, validateConfigShortFlag} {
		cmd := exec.Command(script, filepath.Join(dir, "config.yaml"), flag) //nolint:gosec // test double
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		waitForPidCount(t, filepath.Join(dir, "pids"), 1)
		found, ok := findSuccessorPid(script, 0)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if err := os.Remove(filepath.Join(dir, "pids")); err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Errorf("flag %s: validation run adopted as successor (pid %d)", flag, found)
		}
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
	go func() { done <- waitUntilGone(context.Background(), pid, 0) }()

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

// Cancellation must not cut the drain short: terminateIfAdopted has just sent
// SIGTERM and the process is entitled to its shutdown_timeout before the wrapper
// (PID 1) goes away.
func TestWaitUntilGoneKeepsDrainingAfterCancel(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "while :; do sleep 0.05; done")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	const drain = 600 * time.Millisecond
	started := time.Now()
	done := make(chan error, 1)
	go func() { done <- waitUntilGone(ctx, pid, drain) }()

	select {
	case err := <-done:
		if elapsed := time.Since(started); elapsed < drain {
			t.Fatalf("returned after %v with %v, want at least the %v drain", elapsed, err, drain)
		}
		if err == nil {
			t.Error("giving up on a live process must return an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("waitUntilGone did not return after the drain")
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
	t.Setenv("HUP_LOG", hupLogFor(pidLog))
	t.Setenv("READY_FLAG", filepath.Join(dir, "ready"))

	p := NewProcess(filepath.Join(dir, "config.yaml"), testLogger())
	p.binary = script
	return p, pidLog
}

func hupLogFor(pidLog string) string { return filepath.Join(filepath.Dir(pidLog), "hups") }

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
