package wrapper

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fakeRestarter struct {
	restarts int
}

func (f *fakeRestarter) Restart() error {
	f.restarts++
	return nil
}

func TestRoleWatcherRestartsOnDemotion(t *testing.T) {
	// After a failover the demoted primary keeps serving long-lived client
	// connections through its pooler; only a session drop makes clients
	// reconnect through the Service to the new primary.
	roleFile := filepath.Join(t.TempDir(), "role")
	if err := os.WriteFile(roleFile, []byte("primary"), 0o600); err != nil {
		t.Fatal(err)
	}
	restarter := &fakeRestarter{}
	w := NewRoleWatcher(roleFile, restarter, testLogger())

	w.check(context.Background())
	if restarter.restarts != 0 {
		t.Fatalf("no restart expected while primary, got %d", restarter.restarts)
	}

	if err := os.WriteFile(roleFile, []byte("replica"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.check(context.Background())
	if restarter.restarts != 1 {
		t.Errorf("demotion primary->replica must restart the pooler, got %d restarts", restarter.restarts)
	}

	// No repeated restarts while the role stays replica.
	w.check(context.Background())
	if restarter.restarts != 1 {
		t.Errorf("no repeated restarts expected, got %d", restarter.restarts)
	}
}

func TestRoleWatcherIgnoresPromotionAndEmpty(t *testing.T) {
	roleFile := filepath.Join(t.TempDir(), "role")
	if err := os.WriteFile(roleFile, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	restarter := &fakeRestarter{}
	w := NewRoleWatcher(roleFile, restarter, testLogger())

	// Empty label (pod starting) then promotion replica->primary: no restarts.
	w.check(context.Background())
	if err := os.WriteFile(roleFile, []byte("replica"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.check(context.Background())
	if err := os.WriteFile(roleFile, []byte("primary"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.check(context.Background())

	if restarter.restarts != 0 {
		t.Errorf("promotion/empty transitions must not restart, got %d", restarter.restarts)
	}
}

func TestRoleWatcherMissingFile(t *testing.T) {
	restarter := &fakeRestarter{}
	w := NewRoleWatcher(filepath.Join(t.TempDir(), "absent"), restarter, testLogger())
	w.check(context.Background())
	if restarter.restarts != 0 {
		t.Errorf("missing role file must be a no-op, got %d restarts", restarter.restarts)
	}
}
