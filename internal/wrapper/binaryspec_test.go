package wrapper

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestParseBinarySpec(t *testing.T) {
	data := []byte(`{"url":"https://pg-doorman.cnpg-system.svc:9091","sha256":{"amd64":"aa","arm64":"bb"},"caBundle":"PEM"}`)
	spec, err := ParseBinarySpec(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if spec.URL != "https://pg-doorman.cnpg-system.svc:9091" || spec.SHA256["arm64"] != "bb" || spec.CABundle != "PEM" {
		t.Fatalf("unexpected spec: %+v", spec)
	}
}

func TestParseBinarySpecRejectsInvalid(t *testing.T) {
	if _, err := ParseBinarySpec([]byte(`{`)); err == nil {
		t.Fatal("expected error on malformed JSON")
	}
	if _, err := ParseBinarySpec([]byte(`{"sha256":{"amd64":"aa"}}`)); err == nil {
		t.Fatal("expected error on missing url")
	}
	if _, err := ParseBinarySpec([]byte(`{"url":"https://x"}`)); err == nil {
		t.Fatal("expected error on missing sha256 digests")
	}
}

func TestFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("hello"))
	got, err := FileSHA256(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("hash mismatch: %s", got)
	}
}
