package wrapper

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateConfigBytes_ValidConfig(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    server_port: 5432
    pool_mode: "transaction"
    auth_query:
      query: "SELECT usename, passwd FROM pg_shadow WHERE usename = $1"
      user: "doorman_auth"
      database: "app"
`)
	cfg, err := ValidateConfigBytes(data)
	if err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
	if cfg.General.Host != "0.0.0.0" {
		t.Errorf("expected host 0.0.0.0, got %s", cfg.General.Host)
	}
	if cfg.General.Port != 6432 {
		t.Errorf("expected port 6432, got %d", cfg.General.Port)
	}
	if len(cfg.Pools) != 1 {
		t.Errorf("expected 1 pool, got %d", len(cfg.Pools))
	}
}

func TestValidateConfigBytes_NoPools(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for config with no pools")
	}
	if want := "at least one pool must be defined"; err.Error() != want {
		t.Errorf("expected error %q, got %q", want, err.Error())
	}
}

func TestValidateConfigBytes_PoolWithoutUsersOrAuthQuery(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    server_port: 5432
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for pool without users or auth_query")
	}
}

func TestValidateConfigBytes_AuthQueryMissingQuery(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    auth_query:
      user: "doorman_auth"
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for auth_query without query")
	}
}

func TestValidateConfigBytes_AuthQueryMissingUser(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    auth_query:
      query: "SELECT usename, passwd FROM pg_shadow WHERE usename = $1"
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for auth_query without user")
	}
}

func TestValidateConfigBytes_UserMissingUsername(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    users:
      - password: "secret"
        pool_size: 10
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for user without username")
	}
}

func TestValidateConfigBytes_UserMissingPassword(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    users:
      - username: "app"
        pool_size: 10
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for user without password")
	}
}

func TestValidateConfigBytes_InvalidYAML(t *testing.T) {
	data := []byte(`{{{invalid yaml`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestValidateConfigBytes_MultiplePoolsValid(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    server_port: 5432
    auth_query:
      query: "SELECT usename, passwd FROM pg_shadow WHERE usename = $1"
      user: "doorman_auth"
  admin:
    server_host: "localhost"
    server_port: 5432
    users:
      - username: "admin"
        password: "secret"
`)
	cfg, err := ValidateConfigBytes(data)
	if err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
	if len(cfg.Pools) != 2 {
		t.Errorf("expected 2 pools, got %d", len(cfg.Pools))
	}
}

func TestValidateConfigBytes_PrometheusConfig(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
prometheus:
  enabled: true
  host: "0.0.0.0"
  port: 9127
pools:
  app:
    server_host: "localhost"
    auth_query:
      query: "SELECT usename, passwd FROM pg_shadow WHERE usename = $1"
      user: "doorman_auth"
`)
	cfg, err := ValidateConfigBytes(data)
	if err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
	if cfg.Prometheus == nil {
		t.Fatal("expected prometheus config")
	}
	if !cfg.Prometheus.Enabled {
		t.Error("expected prometheus enabled")
	}
	if cfg.Prometheus.Port != 9127 {
		t.Errorf("expected prometheus port 9127, got %d", cfg.Prometheus.Port)
	}
}

func TestFileHash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte("test content"), 0644); err != nil {
		t.Fatal(err)
	}

	hash1, err := FileHash(path)
	if err != nil {
		t.Fatalf("FileHash failed: %v", err)
	}
	if hash1 == "" {
		t.Fatal("expected non-empty hash")
	}

	// Same content = same hash
	hash2, err := FileHash(path)
	if err != nil {
		t.Fatalf("FileHash failed: %v", err)
	}
	if hash1 != hash2 {
		t.Errorf("expected same hash, got %s and %s", hash1, hash2)
	}

	// Different content = different hash
	if err := os.WriteFile(path, []byte("different content"), 0644); err != nil {
		t.Fatal(err)
	}
	hash3, err := FileHash(path)
	if err != nil {
		t.Fatalf("FileHash failed: %v", err)
	}
	if hash1 == hash3 {
		t.Error("expected different hash for different content")
	}
}

func TestFileHash_NonExistentFile(t *testing.T) {
	_, err := FileHash("/nonexistent/file")
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")

	data := []byte("test data for atomic write")
	if err := atomicWrite(path, data); err != nil {
		t.Fatalf("atomicWrite failed: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("expected %q, got %q", data, got)
	}

	// Verify temp file is cleaned up
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); err == nil {
		t.Error("temp file should be cleaned up after atomic write")
	}
}

func TestValidateAndCopyConfig(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.yaml")
	dst := filepath.Join(dir, "dest.yaml")
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	validConfig := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    auth_query:
      query: "SELECT usename, passwd FROM pg_shadow WHERE usename = $1"
      user: "doorman_auth"
`)
	if err := os.WriteFile(src, validConfig, 0644); err != nil {
		t.Fatal(err)
	}

	if err := ValidateAndCopyConfig(src, dst, logger); err != nil {
		t.Fatalf("ValidateAndCopyConfig failed: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if string(got) != string(validConfig) {
		t.Error("destination content doesn't match source")
	}
}

func TestValidateAndCopyConfig_InvalidConfig(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.yaml")
	dst := filepath.Join(dir, "dest.yaml")
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	invalidConfig := []byte(`
general:
  host: "0.0.0.0"
`)
	if err := os.WriteFile(src, invalidConfig, 0644); err != nil {
		t.Fatal(err)
	}

	if err := ValidateAndCopyConfig(src, dst, logger); err == nil {
		t.Fatal("expected error for invalid config")
	}

	// Destination should not exist
	if _, err := os.Stat(dst); err == nil {
		t.Error("destination should not be created for invalid config")
	}
}

func TestValidateAndCopyConfig_MissingSource(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dest.yaml")
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := ValidateAndCopyConfig("/nonexistent", dst, logger); err == nil {
		t.Fatal("expected error for missing source file")
	}
}
