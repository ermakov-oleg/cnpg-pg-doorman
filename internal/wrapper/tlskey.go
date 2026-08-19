package wrapper

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

// EnsurePKCS8Key converts the private key at srcPath to PKCS#8 PEM at dstPath:
// pg_doorman accepts only PKCS#8, while CNPG issues ECDSA keys in SEC1 PEM.
// The destination is written 0600 and replaced atomically.
func EnsurePKCS8Key(srcPath, dstPath string) error {
	data, err := os.ReadFile(srcPath) //nolint:gosec // path comes from the pod spec env we set ourselves
	if err != nil {
		return fmt.Errorf("cannot read TLS key: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("no PEM block in TLS key %s", srcPath)
	}

	var key any
	switch block.Type {
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	default:
		return fmt.Errorf("unsupported TLS key PEM type %q", block.Type)
	}
	if err != nil {
		return fmt.Errorf("cannot parse TLS key (%s): %w", block.Type, err)
	}

	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("cannot convert TLS key to PKCS#8: %w", err)
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	tmp := dstPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dstPath)
}
