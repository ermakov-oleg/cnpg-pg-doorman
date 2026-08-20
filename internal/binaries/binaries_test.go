package binaries

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifest(t *testing.T) {
	dir := t.TempDir()
	for _, arch := range []string{"amd64", "arm64"} {
		if err := os.MkdirAll(filepath.Join(dir, arch), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, arch, "pg_doorman"), []byte("binary-"+arch), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m) != 2 || m["amd64"] == "" || m["amd64"] == m["arm64"] {
		t.Fatalf("unexpected manifest: %v", m)
	}
}

func TestLoadManifestMissingDir(t *testing.T) {
	m, err := LoadManifest(filepath.Join(t.TempDir(), "nope"))
	if err != nil || m != nil {
		t.Fatalf("expected nil, nil, got %v, %v", m, err)
	}
}
