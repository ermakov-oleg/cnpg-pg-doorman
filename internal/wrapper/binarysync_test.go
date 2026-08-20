package wrapper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func sha(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

func writeSpec(t *testing.T, dir string, spec *BinarySpec) string {
	t.Helper()
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "binary.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newBinaryServer(t *testing.T, body []byte) (url, caPEM string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/binaries/testarch" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	return srv.URL, string(ca)
}

func TestEnsureAtStartupNoSpecCopiesImageBinary(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "image-bin")
	if err := os.WriteFile(image, []byte("image"), 0o755); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(dir, "bin", "pg_doorman")
	s := NewBinarySyncer(filepath.Join(dir, "missing.json"), image, runtimePath, "testarch", slog.Default())
	if _, err := s.EnsureAtStartup(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := os.ReadFile(runtimePath)
	if err != nil || string(got) != "image" {
		t.Fatalf("runtime binary not installed: %v %q", err, got)
	}
	info, _ := os.Stat(runtimePath)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("expected 0755, got %v", info.Mode().Perm())
	}
}

func TestEnsureAtStartupDownloadsDesiredBinary(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "image-bin")
	if err := os.WriteFile(image, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	desired := []byte("new-binary")
	url, ca := newBinaryServer(t, desired)
	specPath := writeSpec(t, dir, &BinarySpec{URL: url, SHA256: map[string]string{"testarch": sha(desired)}, CABundle: ca})
	runtimePath := filepath.Join(dir, "bin", "pg_doorman")
	s := NewBinarySyncer(specPath, image, runtimePath, "testarch", slog.Default())
	if _, err := s.EnsureAtStartup(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(runtimePath)
	if string(got) != "new-binary" {
		t.Fatalf("expected downloaded binary, got %q", got)
	}
}

func TestEnsureAtStartupRejectsDigestMismatch(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "image-bin")
	if err := os.WriteFile(image, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	url, ca := newBinaryServer(t, []byte("tampered"))
	specPath := writeSpec(t, dir, &BinarySpec{URL: url, SHA256: map[string]string{"testarch": sha([]byte("expected"))}, CABundle: ca})
	runtimePath := filepath.Join(dir, "bin", "pg_doorman")
	s := NewBinarySyncer(specPath, image, runtimePath, "testarch", slog.Default())
	// Availability first: startup falls back to the image binary on failure.
	if _, err := s.EnsureAtStartup(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(runtimePath)
	if string(got) != "old" {
		t.Fatalf("expected image binary fallback, got %q", got)
	}
}

func TestEnsureAtStartupSkipsWhenAlreadyDesired(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "image-bin")
	body := []byte("current")
	if err := os.WriteFile(image, body, 0o755); err != nil {
		t.Fatal(err)
	}
	// URL points nowhere: must not be contacted when hashes already match.
	specPath := writeSpec(t, dir, &BinarySpec{URL: "https://127.0.0.1:1", SHA256: map[string]string{"testarch": sha(body)}})
	runtimePath := filepath.Join(dir, "bin", "pg_doorman")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, body, 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewBinarySyncer(specPath, image, runtimePath, "testarch", slog.Default())
	if _, err := s.EnsureAtStartup(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
