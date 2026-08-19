package wrapper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const (
	fwOldConfig = "general:\n  worker_threads: 2\npools:\n  db:\n    server_host: h\n    users:\n      - username: u\n        password: p\n"
	fwNewConfig = "general:\n  worker_threads: 2\npools:\n  db:\n    server_host: h\n    pool_mode: session\n    users:\n      - username: u\n        password: p\n"
	fwThreads8  = "general:\n  worker_threads: 8\npools:\n  db:\n    server_host: h\n    users:\n      - username: u\n        password: p\n"
)

type fwReloader struct {
	reloads  int
	restarts int
}

func (f *fwReloader) Reload() error  { f.reloads++; return nil }
func (f *fwReloader) Restart() error { f.restarts++; return nil }

func newTestFileWatcher(t *testing.T, tester ConfigTester) (*FileWatcher, *fwReloader, string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "mounted.yaml")
	runtimePath := filepath.Join(dir, "pg_doorman.yaml")
	if err := os.WriteFile(source, []byte(fwOldConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	reloader := &fwReloader{}
	fw := NewFileWatcher(source, runtimePath, "", "", reloader, testLogger())
	fw.testConfig = tester
	return fw, reloader, source, runtimePath
}

func TestFileWatcherApplyInitialAndReload(t *testing.T) {
	tester := func(_ context.Context, _ string) error { return nil }
	fw, reloader, source, runtimePath := newTestFileWatcher(t, tester)

	if err := fw.ApplyInitial(context.Background()); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(runtimePath); string(data) != fwOldConfig {
		t.Fatalf("runtime config not materialized: %q", data)
	}

	// No change: no reload.
	fw.check(context.Background())
	if reloader.reloads+reloader.restarts != 0 {
		t.Fatalf("no-op check must not signal the process")
	}

	// Reloadable change: SIGHUP path.
	if err := os.WriteFile(source, []byte(fwNewConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	fw.check(context.Background())
	if reloader.reloads != 1 || reloader.restarts != 0 {
		t.Errorf("reloads=%d restarts=%d, want 1/0", reloader.reloads, reloader.restarts)
	}
	if data, _ := os.ReadFile(runtimePath); string(data) != fwNewConfig {
		t.Errorf("runtime config not updated")
	}
}

func TestFileWatcherRestartsOnNonReloadableChange(t *testing.T) {
	tester := func(_ context.Context, _ string) error { return nil }
	fw, reloader, source, _ := newTestFileWatcher(t, tester)
	if err := fw.ApplyInitial(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(source, []byte(fwThreads8), 0o600); err != nil {
		t.Fatal(err)
	}
	fw.check(context.Background())
	if reloader.restarts != 1 || reloader.reloads != 0 {
		t.Errorf("worker_threads change must restart, got reloads=%d restarts=%d", reloader.reloads, reloader.restarts)
	}
}

func TestFileWatcherKeepsOldConfigWhenBinaryRejects(t *testing.T) {
	calls := 0
	tester := func(_ context.Context, _ string) error {
		calls++
		if calls > 1 {
			return errors.New("rejected")
		}
		return nil
	}
	fw, reloader, source, runtimePath := newTestFileWatcher(t, tester)
	if err := fw.ApplyInitial(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(source, []byte(fwNewConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	fw.check(context.Background())
	if reloader.reloads+reloader.restarts != 0 {
		t.Error("rejected config must not signal the process")
	}
	if data, _ := os.ReadFile(runtimePath); string(data) != fwOldConfig {
		t.Errorf("runtime config must keep the last-good content, got %q", data)
	}

	// Change is retried on the next poll (lastApplied not advanced).
	fw.check(context.Background())
	if calls < 3 {
		t.Errorf("rejected change must be retried, tester calls = %d", calls)
	}
}
