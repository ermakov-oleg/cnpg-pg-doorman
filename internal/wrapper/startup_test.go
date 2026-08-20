package wrapper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// newStartupFixture wires a syncer and a file watcher over tmpdir paths. The
// config tester accepts only while the runtime binary holds acceptedBy, standing
// in for a binary whose config schema differs from the rendered one.
func newStartupFixture(t *testing.T, installed, acceptedBy string) (*FileWatcher, *BinarySyncer, string) {
	t.Helper()
	dir := t.TempDir()
	image := filepath.Join(dir, "image-bin")
	if err := os.WriteFile(image, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeBin := filepath.Join(dir, "bin", "pg_doorman")
	if err := os.MkdirAll(filepath.Dir(runtimeBin), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimeBin, []byte(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	syncer := NewBinarySyncer(filepath.Join(dir, "binary.json"), image, runtimeBin, "testarch", testLogger())

	source := filepath.Join(dir, "mounted.yaml")
	if err := os.WriteFile(source, []byte(fwOldConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	fw := NewFileWatcher(source, filepath.Join(dir, "pg_doorman.yaml"), "", "", &fwReloader{}, testLogger())
	fw.testConfig = func(_ context.Context, _ string) error {
		data, err := os.ReadFile(runtimeBin)
		if err != nil {
			return err
		}
		if string(data) != acceptedBy {
			return errors.New("config rejected")
		}
		return nil
	}
	return fw, syncer, runtimeBin
}

func TestApplyInitialConfigKeepsSeedWhenConfigAccepted(t *testing.T) {
	fw, syncer, _ := newStartupFixture(t, "delivered", "delivered")
	seed := []byte(`{"url":"https://example"}`)

	got, err := ApplyInitialConfig(context.Background(), fw, syncer, seed, testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(seed) {
		t.Errorf("seed = %q, want it untouched", got)
	}
	if data, _ := os.ReadFile(fw.runtimePath); string(data) != fwOldConfig {
		t.Errorf("runtime config = %q, want it materialized", data)
	}
}

func TestApplyInitialConfigRevertsToImageWhenConfigRejected(t *testing.T) {
	fw, syncer, runtimeBin := newStartupFixture(t, "delivered", "image")
	BinaryStale.Set(0)

	seed, err := ApplyInitialConfig(context.Background(), fw, syncer, []byte(`{"url":"https://example"}`), testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seed != nil {
		t.Error("seed must be nil after a revert: the watcher has to retry the desired spec")
	}
	if got, _ := os.ReadFile(runtimeBin); string(got) != "image" {
		t.Errorf("runtime binary = %q, want the image binary", got)
	}
	if data, _ := os.ReadFile(fw.runtimePath); string(data) != fwOldConfig {
		t.Errorf("runtime config = %q, want it materialized on the retry", data)
	}
	if got := testutil.ToFloat64(BinaryStale); got != 1 {
		t.Errorf("binary_stale = %v, want 1: the desired binary is not the running one", got)
	}
}

// Nothing to revert to: the config itself is broken and the wrapper must fail
// instead of pretending the image binary is a fallback.
func TestApplyInitialConfigFailsWhenAlreadyOnImageBinary(t *testing.T) {
	fw, syncer, _ := newStartupFixture(t, "image", "something-else")

	if _, err := ApplyInitialConfig(context.Background(), fw, syncer, nil, testLogger()); err == nil {
		t.Fatal("expected an error when the image binary also rejects the config")
	}
}
