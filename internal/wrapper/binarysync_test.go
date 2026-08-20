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
	"strconv"
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
	seed, err := s.EnsureAtStartup(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(runtimePath)
	if string(got) != "new-binary" {
		t.Fatalf("expected downloaded binary, got %q", got)
	}
	if seed == nil {
		t.Error("seed must carry the spec: the desired binary is installed")
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
	seed, err := s.EnsureAtStartup(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := os.ReadFile(runtimePath)
	if string(got) != "old" {
		t.Fatalf("expected image binary fallback, got %q", got)
	}
	if seed != nil {
		t.Error("seed must be nil after a fallback: the watcher has to retry the unsatisfied spec")
	}
}

func TestRevertToImageReinstallsImageBinary(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "image-bin")
	if err := os.WriteFile(image, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(dir, "bin", "pg_doorman")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, []byte("delivered"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewBinarySyncer(filepath.Join(dir, "binary.json"), image, runtimePath, "testarch", testLogger())

	reverted, err := s.RevertToImage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reverted {
		t.Error("reverted = false, want true: the installed binary was not the image one")
	}
	if got, _ := os.ReadFile(runtimePath); string(got) != "image" {
		t.Errorf("runtime binary = %q, want the image binary", got)
	}
	if info, _ := os.Stat(runtimePath); info.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
}

func TestRevertToImageIsNoopWhenAlreadyImage(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "image-bin")
	if err := os.WriteFile(image, []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(dir, "bin", "pg_doorman")
	if err := os.MkdirAll(filepath.Dir(runtimePath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimePath, []byte("image"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewBinarySyncer(filepath.Join(dir, "binary.json"), image, runtimePath, "testarch", testLogger())

	reverted, err := s.RevertToImage()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reverted {
		t.Error("reverted = true, want false: there is nothing left to fall back to")
	}
}

func TestDownloadRejectsDeclaredOversize(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxBinaryBytes+1, 10))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	spec := &BinarySpec{URL: srv.URL, SHA256: map[string]string{"testarch": sha([]byte("x"))}, CABundle: string(ca)}
	dest := filepath.Join(dir, "bin", "pg_doorman")
	s := NewBinarySyncer(filepath.Join(dir, "binary.json"), filepath.Join(dir, "image-bin"), dest, "testarch", testLogger())

	err := s.Download(context.Background(), spec, spec.SHA256["testarch"], dest)
	if err == nil {
		t.Fatal("expected an error for an oversized Content-Length")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Error("nothing may be installed after a rejected download")
	}
}

func TestDownloadRejectsOversizedBody(t *testing.T) {
	restore := maxBinaryBytes
	maxBinaryBytes = 8
	t.Cleanup(func() { maxBinaryBytes = restore })

	dir := t.TempDir()
	// Flushing between writes drops Content-Length, so only the copy limit
	// stands between a hostile endpoint and the tmpfs runtime dir.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("12345"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("67890"))
	}))
	defer srv.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	spec := &BinarySpec{URL: srv.URL, SHA256: map[string]string{"testarch": sha([]byte("1234567890"))}, CABundle: string(ca)}
	dest := filepath.Join(dir, "bin", "pg_doorman")
	s := NewBinarySyncer(filepath.Join(dir, "binary.json"), filepath.Join(dir, "image-bin"), dest, "testarch", testLogger())

	err := s.Download(context.Background(), spec, spec.SHA256["testarch"], dest)
	if err == nil {
		t.Fatal("expected an error for a body over the size cap")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Error("nothing may be installed after a rejected download")
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
	seed, err := s.EnsureAtStartup(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seed == nil {
		t.Error("seed must carry the spec: the runtime binary already matches it")
	}
}
