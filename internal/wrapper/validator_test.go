package wrapper

import (
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

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")

	data := []byte("test data for atomic write")
	if err := AtomicWrite(path, data); err != nil {
		t.Fatalf("AtomicWrite failed: %v", err)
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

func TestValidateConfigBytes_InvalidPoolMode(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    pool_mode: "statement"
    users:
      - username: "app"
        password: "secret"
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for invalid pool_mode")
	}
}

func TestValidateConfigBytes_ZeroWorkerThreads(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
  worker_threads: 0
pools:
  app:
    server_host: "localhost"
    users:
      - username: "app"
        password: "secret"
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for zero worker_threads")
	}
}

func TestValidateConfigBytes_ZeroMaxConnections(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
  max_connections: 0
pools:
  app:
    server_host: "localhost"
    users:
      - username: "app"
        password: "secret"
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for zero max_connections")
	}
}

func TestValidateConfigBytes_InvalidDuration(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
  connect_timeout: "3 seconds"
pools:
  app:
    server_host: "localhost"
    users:
      - username: "app"
        password: "secret"
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for invalid connect_timeout")
	}
}

func TestValidateConfigBytes_ValidDurations(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
  connect_timeout: "3000"
  idle_timeout: "5m"
  server_lifetime: "1H"
  shutdown_timeout: "10s"
pools:
  app:
    server_host: "localhost"
    users:
      - username: "app"
        password: "secret"
`)
	_, err := ValidateConfigBytes(data)
	if err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
}

func TestValidateConfigBytes_ZeroUserPoolSize(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    users:
      - username: "app"
        password: "secret"
        pool_size: 0
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for zero users[].pool_size")
	}
}

func TestValidateConfigBytes_ZeroAuthQueryWorkers(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    auth_query:
      query: "SELECT usename, passwd FROM pg_shadow WHERE usename = $1"
      user: "doorman_auth"
      workers: 0
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for zero auth_query.workers")
	}
}

func TestValidateConfigBytes_InvalidAuthQueryCacheTTL(t *testing.T) {
	data := []byte(`
general:
  host: "0.0.0.0"
  port: 6432
pools:
  app:
    server_host: "localhost"
    auth_query:
      query: "SELECT usename, passwd FROM pg_shadow WHERE usename = $1"
      user: "doorman_auth"
      cache_ttl: "1w"
`)
	_, err := ValidateConfigBytes(data)
	if err == nil {
		t.Fatal("expected error for invalid auth_query.cache_ttl")
	}
}

func TestAtomicWriteMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.yaml")
	if err := AtomicWrite(path, []byte("x")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config file mode = %o, want 600 (plaintext passwords inside)", info.Mode().Perm())
	}
}
