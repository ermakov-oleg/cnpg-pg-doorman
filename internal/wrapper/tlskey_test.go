package wrapper

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatal(err)
	}
}

func assertPKCS8(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		t.Fatalf("expected PKCS#8 PRIVATE KEY block, got %+v", block)
	}
	if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
		t.Fatalf("result is not valid PKCS#8: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %o, want 600", info.Mode().Perm())
	}
}

func TestEnsurePKCS8KeyFromEC(t *testing.T) {
	// CNPG issues ECDSA keys in SEC1 PEM; pg_doorman only accepts PKCS#8.
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "tls.key")
	dst := filepath.Join(dir, "converted.key")
	writePEM(t, src, "EC PRIVATE KEY", der)

	if err := EnsurePKCS8Key(src, dst); err != nil {
		t.Fatal(err)
	}
	assertPKCS8(t, dst)
}

func TestEnsurePKCS8KeyFromPKCS1(t *testing.T) {
	dir := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "tls.key")
	dst := filepath.Join(dir, "converted.key")
	writePEM(t, src, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))

	if err := EnsurePKCS8Key(src, dst); err != nil {
		t.Fatal(err)
	}
	assertPKCS8(t, dst)
}

func TestEnsurePKCS8KeyPassthrough(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "tls.key")
	dst := filepath.Join(dir, "converted.key")
	writePEM(t, src, "PRIVATE KEY", der)

	if err := EnsurePKCS8Key(src, dst); err != nil {
		t.Fatal(err)
	}
	assertPKCS8(t, dst)
}
