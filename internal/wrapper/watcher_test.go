package wrapper

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
)

type fakeReloader struct {
	calls    int
	restarts int
	err      error
}

func (f *fakeReloader) Reload() error {
	f.calls++
	return f.err
}

func (f *fakeReloader) Restart() error {
	f.restarts++
	return f.err
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func newTestWatcher(t *testing.T, tester ConfigTester, reloader Reloader) (*CRDWatcher, string) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cr := &v1alpha1.PgDoorman{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-doorman",
			Namespace:  "ns",
			Generation: 2,
			UID:        "original-uid",
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).Build()

	runtimePath := filepath.Join(t.TempDir(), "pg_doorman.yaml")
	if err := os.WriteFile(runtimePath, []byte("old: config"), 0o600); err != nil {
		t.Fatal(err)
	}

	generate := func(_ context.Context, _ *v1alpha1.PgDoormanSpec) ([]byte, error) {
		return []byte("general:\n  host: 0.0.0.0\npools:\n  db:\n    server_host: h\n    users:\n      - username: u\n        password: p\n"), nil
	}

	w := NewCRDWatcher(cl, "test-doorman", "ns", runtimePath, reloader, generate, nil, testLogger(), 1, "")
	w.testConfig = tester
	return w, runtimePath
}

func TestCheckRejectedConfigKeepsOldFileAndRetries(t *testing.T) {
	// If pg_doorman rejects the candidate config, the runtime file must keep
	// the last-good content (a poisoned file would crash-loop the process on
	// any later restart), no SIGHUP must be sent, and the change must be
	// retried on the next poll instead of being marked as applied.
	reloader := &fakeReloader{}
	testerCalls := 0
	tester := func(_ context.Context, _ string) error {
		testerCalls++
		return errors.New("parse error at line 3")
	}
	w, runtimePath := newTestWatcher(t, tester, reloader)

	w.check(context.Background())
	w.check(context.Background())

	if reloader.calls != 0 {
		t.Errorf("Reload called %d times, want 0", reloader.calls)
	}
	if testerCalls != 2 {
		t.Errorf("config tester called %d times, want 2 (change must be retried)", testerCalls)
	}
	data, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old: config" {
		t.Errorf("runtime config was overwritten with a rejected config: %q", data)
	}
	if _, err := os.Stat(runtimePath + CandidateSuffix); !os.IsNotExist(err) {
		t.Errorf("candidate file must be removed after rejection, stat err = %v", err)
	}
}

func TestCheckAcceptedConfigReloadsOnce(t *testing.T) {
	reloader := &fakeReloader{}
	var testedPath string
	tester := func(_ context.Context, path string) error {
		testedPath = path
		return nil
	}
	w, runtimePath := newTestWatcher(t, tester, reloader)

	w.check(context.Background())

	if reloader.calls != 1 {
		t.Fatalf("Reload called %d times, want 1", reloader.calls)
	}
	if testedPath != runtimePath+CandidateSuffix {
		t.Errorf("binary test ran on %q, want candidate path %q", testedPath, runtimePath+CandidateSuffix)
	}
	data, err := os.ReadFile(runtimePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "old: config" {
		t.Error("runtime config was not replaced with the accepted config")
	}

	// Generation is now recorded: the next poll must be a no-op.
	w.check(context.Background())
	if reloader.calls != 1 {
		t.Errorf("Reload called %d times after no-op poll, want 1", reloader.calls)
	}
}

func TestNewBinaryConfigTester(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	okBin := filepath.Join(dir, "ok.sh")
	if err := os.WriteFile(okBin, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	failBin := filepath.Join(dir, "fail.sh")
	if err := os.WriteFile(failBin, []byte("#!/bin/sh\necho 'bad config' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := NewBinaryConfigTester(okBin)(context.Background(), cfgPath); err != nil {
		t.Errorf("expected success from ok binary, got %v", err)
	}
	err := NewBinaryConfigTester(failBin)(context.Background(), cfgPath)
	if err == nil {
		t.Fatal("expected error from failing binary")
	}
	if !strings.Contains(err.Error(), "bad config") {
		t.Errorf("error must include binary output, got %q", err.Error())
	}
}

func TestCheckNonReloadableChangeRestartsProcess(t *testing.T) {
	// worker_threads sizes the tokio runtime at startup: SIGHUP does not apply
	// it. The wrapper must gracefully restart the process instead of logging a
	// false "config reloaded successfully".
	reloader := &fakeReloader{}
	tester := func(_ context.Context, _ string) error { return nil }
	w, runtimePath := newTestWatcher(t, tester, reloader)

	oldCfg := "general:\n  worker_threads: 2\npools:\n  db:\n    server_host: h\n    users:\n      - username: u\n        password: p\n"
	if err := os.WriteFile(runtimePath, []byte(oldCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	w.generate = func(_ context.Context, _ *v1alpha1.PgDoormanSpec) ([]byte, error) {
		return []byte("general:\n  worker_threads: 8\npools:\n  db:\n    server_host: h\n    users:\n      - username: u\n        password: p\n"), nil
	}

	w.check(context.Background())

	if reloader.restarts != 1 {
		t.Errorf("Restart called %d times, want 1", reloader.restarts)
	}
	if reloader.calls != 0 {
		t.Errorf("Reload called %d times, want 0 (SIGHUP cannot apply worker_threads)", reloader.calls)
	}
}

func TestCheckReloadableChangeDoesNotRestart(t *testing.T) {
	reloader := &fakeReloader{}
	tester := func(_ context.Context, _ string) error { return nil }
	w, runtimePath := newTestWatcher(t, tester, reloader)

	oldCfg := "general:\n  worker_threads: 2\npools:\n  db:\n    server_host: h\n    pool_mode: session\n    users:\n      - username: u\n        password: p\n"
	if err := os.WriteFile(runtimePath, []byte(oldCfg), 0o600); err != nil {
		t.Fatal(err)
	}
	w.generate = func(_ context.Context, _ *v1alpha1.PgDoormanSpec) ([]byte, error) {
		return []byte("general:\n  worker_threads: 2\npools:\n  db:\n    server_host: h\n    pool_mode: transaction\n    users:\n      - username: u\n        password: p\n"), nil
	}

	w.check(context.Background())

	if reloader.calls != 1 {
		t.Errorf("Reload called %d times, want 1", reloader.calls)
	}
	if reloader.restarts != 0 {
		t.Errorf("Restart called %d times, want 0", reloader.restarts)
	}
}

func TestNeedsProcessRestart(t *testing.T) {
	mk := func(threads, maxConn, port int) *DoormanConfig {
		return &DoormanConfig{General: GeneralConfig{
			WorkerThreads:  &threads,
			MaxConnections: &maxConn,
			Host:           "0.0.0.0",
			Port:           port,
		}}
	}
	if NeedsProcessRestart(mk(4, 100, 6432), mk(4, 100, 6432)) {
		t.Error("identical non-reloadable fields must not require restart")
	}
	for name, changed := range map[string]*DoormanConfig{
		"worker_threads":  mk(8, 100, 6432),
		"max_connections": mk(4, 200, 6432),
		"port":            mk(4, 100, 7432),
	} {
		base := mk(4, 100, 6432)
		if !NeedsProcessRestart(base, changed) {
			t.Errorf("%s change must require restart", name)
		}
	}
}

func TestCheckDetectsRecreatedCRWithSameGeneration(t *testing.T) {
	// kubectl replace --force / GitOps prune+recreate produces a NEW object
	// with generation=1; if the old one was also generation=1 and secret refs
	// did not change, the new spec must still be applied — the watcher tracks
	// the object UID, not only the generation.
	reloader := &fakeReloader{}
	tester := func(_ context.Context, _ string) error { return nil }
	w, _ := newTestWatcher(t, tester, reloader)

	// First check adopts the current object (gen=2 vs lastGen=1 → reload).
	w.check(context.Background())
	if reloader.calls != 1 {
		t.Fatalf("setup reload expected, got %d", reloader.calls)
	}

	// Replace the object with a new UID but the same generation.
	var old v1alpha1.PgDoorman
	if err := w.client.Get(context.Background(), client.ObjectKey{Name: "test-doorman", Namespace: "ns"}, &old); err != nil {
		t.Fatal(err)
	}
	if err := w.client.Delete(context.Background(), &old); err != nil {
		t.Fatal(err)
	}
	recreated := &v1alpha1.PgDoorman{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-doorman",
			Namespace:  "ns",
			Generation: 2,
			UID:        "recreated-uid",
		},
	}
	if err := w.client.Create(context.Background(), recreated); err != nil {
		t.Fatal(err)
	}

	w.check(context.Background())
	if reloader.calls != 2 {
		t.Errorf("recreated CR with same generation must trigger a reload, got %d calls", reloader.calls)
	}

	// And no spurious reload afterwards.
	w.check(context.Background())
	if reloader.calls != 2 {
		t.Errorf("no further reloads expected, got %d", reloader.calls)
	}
}
