//go:build linux

package wrapper

import (
	"io"
	"log/slog"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCapFileDescriptorLimit(t *testing.T) {
	var original unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &original); err != nil {
		t.Fatalf("getrlimit: %v", err)
	}
	t.Cleanup(func() {
		_ = unix.Setrlimit(unix.RLIMIT_NOFILE, &original)
	})

	if original.Max <= MaxNoFile {
		t.Skipf("hard limit %d leaves nothing to cap", original.Max)
	}
	raised := unix.Rlimit{Cur: MaxNoFile * 2, Max: original.Max}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &raised); err != nil {
		t.Skipf("cannot raise the soft limit: %v", err)
	}

	CapFileDescriptorLimit(slog.New(slog.NewTextHandler(io.Discard, nil)))

	var current unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &current); err != nil {
		t.Fatalf("getrlimit: %v", err)
	}
	if current.Cur != MaxNoFile {
		t.Errorf("soft limit = %d, want %d", current.Cur, MaxNoFile)
	}
	if current.Max != original.Max {
		t.Errorf("hard limit = %d, want it untouched at %d", current.Max, original.Max)
	}
}

func TestCapFileDescriptorLimitKeepsLowerLimit(t *testing.T) {
	var original unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &original); err != nil {
		t.Fatalf("getrlimit: %v", err)
	}
	t.Cleanup(func() {
		_ = unix.Setrlimit(unix.RLIMIT_NOFILE, &original)
	})

	lowered := unix.Rlimit{Cur: 512, Max: original.Max}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &lowered); err != nil {
		t.Skipf("cannot lower the soft limit: %v", err)
	}

	CapFileDescriptorLimit(slog.New(slog.NewTextHandler(io.Discard, nil)))

	var current unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &current); err != nil {
		t.Fatalf("getrlimit: %v", err)
	}
	if current.Cur != 512 {
		t.Errorf("soft limit = %d, want the existing 512 to be kept", current.Cur)
	}
}
