package wrapper

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeUpgrader struct {
	upgrades int
	err      error
}

func (f *fakeUpgrader) Upgrade() error {
	f.upgrades++
	return f.err
}

func acceptingTester(_ context.Context, _ string) error { return nil }

// newTestBinaryWatcher wires a watcher over tmpdir paths; the runtime binary
// content, when non-empty, stands for the currently installed pg_doorman.
func newTestBinaryWatcher(t *testing.T, dir, specPath, installed string, tester ConfigTester) (*BinaryWatcher, *fakeUpgrader) {
	t.Helper()
	runtimePath := filepath.Join(dir, "bin", "pg_doorman")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o750); err != nil {
		t.Fatal(err)
	}
	if installed != "" {
		if err := os.WriteFile(runtimePath, []byte(installed), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	imagePath := filepath.Join(dir, "image-bin")
	if err := os.WriteFile(imagePath, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "pg_doorman.yaml")
	if err := os.WriteFile(configPath, []byte(fwOldConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	up := &fakeUpgrader{}
	syncer := NewBinarySyncer(specPath, imagePath, runtimePath, "testarch", testLogger())
	w := NewBinaryWatcher(specPath, syncer, up, testLogger())
	w.runtimePath = runtimePath
	w.configPath = configPath
	w.arch = "testarch"
	w.testConfig = tester
	return w, up
}

func TestBinaryWatcherUnchangedSpecIsNoop(t *testing.T) {
	dir := t.TempDir()
	desired := []byte("new-binary")
	// URL points nowhere: an unchanged spec must not be acted on at all.
	specPath := writeSpec(t, dir, &BinarySpec{URL: "https://127.0.0.1:1", SHA256: map[string]string{"testarch": sha(desired)}})
	w, up := newTestBinaryWatcher(t, dir, specPath, "old", acceptingTester)

	data, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	w.Seed(data)
	BinaryStale.Set(0)
	w.check(context.Background())

	if up.upgrades != 0 {
		t.Errorf("upgrades = %d, want 0", up.upgrades)
	}
	// The spec is unsatisfied but unchanged: nothing at all may happen, and a
	// download attempt would have marked the binary stale.
	if got := testutil.ToFloat64(BinaryStale); got != 0 {
		t.Errorf("binary_stale = %v, want 0: an unchanged spec must not be acted on", got)
	}
}

func TestBinaryWatcherSkipsWhenAlreadyDesired(t *testing.T) {
	dir := t.TempDir()
	installed := "current-binary"
	specPath := writeSpec(t, dir, &BinarySpec{URL: "https://127.0.0.1:1", SHA256: map[string]string{"testarch": sha([]byte(installed))}})
	w, up := newTestBinaryWatcher(t, dir, specPath, installed, acceptingTester)
	BinaryStale.Set(1)

	w.check(context.Background())

	if up.upgrades != 0 {
		t.Errorf("upgrades = %d, want 0", up.upgrades)
	}
	if w.lastSpec == nil {
		t.Error("lastSpec must be advanced: the spec is satisfied, retrying is pointless")
	}
	if got := testutil.ToFloat64(BinaryStale); got != 0 {
		t.Errorf("binary_stale = %v, want 0", got)
	}
}

func TestBinaryWatcherDownloadsValidatesAndUpgrades(t *testing.T) {
	dir := t.TempDir()
	desired := []byte("new-binary")
	url, ca := newBinaryServer(t, desired)
	specPath := writeSpec(t, dir, &BinarySpec{URL: url, SHA256: map[string]string{"testarch": sha(desired)}, CABundle: ca})
	w, up := newTestBinaryWatcher(t, dir, specPath, "old", acceptingTester)
	var logs bytes.Buffer
	w.logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))

	w.check(context.Background())

	if up.upgrades != 1 {
		t.Fatalf("upgrades = %d, want 1", up.upgrades)
	}
	if got, _ := os.ReadFile(w.runtimePath); !bytes.Equal(got, desired) {
		t.Errorf("runtime binary = %q, want %q", got, desired)
	}
	if _, err := os.Stat(w.runtimePath + binaryCandidateSuffix); !os.IsNotExist(err) {
		t.Error("candidate file must be renamed onto the runtime path")
	}
	if w.lastSpec == nil {
		t.Error("lastSpec must be advanced after a successful upgrade")
	}
	if got := testutil.ToFloat64(BinaryStale); got != 0 {
		t.Errorf("binary_stale = %v, want 0", got)
	}
	// The e2e suite matches this line to detect a live upgrade.
	if !strings.Contains(logs.String(), "in-place binary upgrade triggered") {
		t.Errorf("missing upgrade log line, got: %s", logs.String())
	}

	// lastSpec was advanced by the check above, not seeded: dropping the runtime
	// binary would otherwise make the second check download and upgrade again.
	if err := os.Remove(w.runtimePath); err != nil {
		t.Fatal(err)
	}
	w.check(context.Background())
	if up.upgrades != 1 {
		t.Errorf("upgrades = %d after an unchanged spec, want 1", up.upgrades)
	}
	if _, err := os.Stat(w.runtimePath); !os.IsNotExist(err) {
		t.Error("the second check must not act on an unchanged spec")
	}
}

func TestBinaryWatcherKeepsBinaryOnDigestMismatch(t *testing.T) {
	dir := t.TempDir()
	url, ca := newBinaryServer(t, []byte("tampered"))
	specPath := writeSpec(t, dir, &BinarySpec{URL: url, SHA256: map[string]string{"testarch": sha([]byte("expected"))}, CABundle: ca})
	w, up := newTestBinaryWatcher(t, dir, specPath, "old", acceptingTester)
	BinaryStale.Set(0)

	w.check(context.Background())

	if up.upgrades != 0 {
		t.Errorf("upgrades = %d, want 0", up.upgrades)
	}
	if got, _ := os.ReadFile(w.runtimePath); string(got) != "old" {
		t.Errorf("runtime binary = %q, want it untouched", got)
	}
	if got := testutil.ToFloat64(BinaryStale); got != 1 {
		t.Errorf("binary_stale = %v, want 1", got)
	}
	if w.lastSpec != nil {
		t.Error("lastSpec must not advance: the change has to be retried on the next poll")
	}
}

func TestBinaryWatcherKeepsBinaryWhenCandidateRejectsConfig(t *testing.T) {
	dir := t.TempDir()
	desired := []byte("new-binary")
	url, ca := newBinaryServer(t, desired)
	specPath := writeSpec(t, dir, &BinarySpec{URL: url, SHA256: map[string]string{"testarch": sha(desired)}, CABundle: ca})
	rejecting := func(_ context.Context, _ string) error { return errors.New("rejected") }
	w, up := newTestBinaryWatcher(t, dir, specPath, "old", rejecting)

	w.check(context.Background())

	if up.upgrades != 0 {
		t.Errorf("upgrades = %d, want 0", up.upgrades)
	}
	if got, _ := os.ReadFile(w.runtimePath); string(got) != "old" {
		t.Errorf("runtime binary = %q, want it untouched", got)
	}
	if _, err := os.Stat(w.runtimePath + binaryCandidateSuffix); !os.IsNotExist(err) {
		t.Error("rejected candidate must be removed")
	}
	if w.lastSpec != nil {
		t.Error("lastSpec must not advance after a rejected candidate")
	}
}
