package configgen

import (
	"strings"
	"testing"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/wrapper"

	"gopkg.in/yaml.v3"
	"k8s.io/utils/ptr"
)

func TestGenerate_MinimalAuthQuery(t *testing.T) {
	spec := &v1alpha1.PgDoormanSpec{
		Pools: map[string]v1alpha1.PoolSpec{
			"app": {
				AuthQuery: &v1alpha1.AuthQuerySpec{
					User: "doorman_auth",
				},
			},
		},
	}

	data, err := Generate(spec, 6432, 9127, nil, nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	var cfg wrapper.DoormanConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if cfg.General.Host != "0.0.0.0" {
		t.Errorf("expected host 0.0.0.0, got %q", cfg.General.Host)
	}
	if cfg.General.Port != 6432 {
		t.Errorf("expected port 6432, got %d", cfg.General.Port)
	}
	if *cfg.General.WorkerThreads != 4 {
		t.Errorf("expected 4 worker threads, got %d", *cfg.General.WorkerThreads)
	}
	if *cfg.General.MaxConnections != 8192 {
		t.Errorf("expected 8192 max connections, got %d", *cfg.General.MaxConnections)
	}

	pool, ok := cfg.Pools["app"]
	if !ok {
		t.Fatal("expected pool 'app'")
	}
	if pool.ServerHost != "localhost" {
		t.Errorf("expected server_host localhost, got %q", pool.ServerHost)
	}
	if pool.ServerPort != 5432 {
		t.Errorf("expected server_port 5432, got %d", pool.ServerPort)
	}
	if pool.PoolMode != "transaction" {
		t.Errorf("expected pool_mode transaction, got %q", pool.PoolMode)
	}
	if pool.AuthQuery == nil {
		t.Fatal("expected auth_query")
	}
	if pool.AuthQuery.User != "doorman_auth" {
		t.Errorf("expected user doorman_auth, got %q", pool.AuthQuery.User)
	}
	if pool.AuthQuery.Query != v1alpha1.DefaultAuthQueryQuery {
		t.Errorf("expected default query, got %q", pool.AuthQuery.Query)
	}
	if *pool.AuthQuery.PoolSize != 40 {
		t.Errorf("expected pool_size 40, got %d", *pool.AuthQuery.PoolSize)
	}
	if *pool.AuthQuery.Workers != 2 {
		t.Errorf("expected workers 2, got %d", *pool.AuthQuery.Workers)
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

func TestGenerate_AuthQueryKeysMatchUpstream(t *testing.T) {
	spec := &v1alpha1.PgDoormanSpec{
		Pools: map[string]v1alpha1.PoolSpec{
			"app": {
				DefaultPoolSize: ptr.To(40),
				AuthQuery: &v1alpha1.AuthQuerySpec{
					User:     "doorman_auth",
					PoolSize: ptr.To(5),
				},
			},
		},
	}

	data, err := Generate(spec, 6432, 9127, nil, nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	aq := raw["pools"].(map[string]any)["app"].(map[string]any)["auth_query"].(map[string]any)

	// pg_doorman v3.11.0 (upstream PR #148): workers = executor connections, pool_size = dynamic user data pool
	if got := aq["workers"]; got != 5 {
		t.Errorf("expected auth_query.workers 5, got %v", got)
	}
	if got := aq["pool_size"]; got != 40 {
		t.Errorf("expected auth_query.pool_size 40, got %v", got)
	}
	if _, ok := aq["default_pool_size"]; ok {
		t.Error("auth_query.default_pool_size must not be written: key removed in pg_doorman v3.11.0")
	}
}

func TestGenerate_WithPasswords(t *testing.T) {
	spec := &v1alpha1.PgDoormanSpec{
		Pools: map[string]v1alpha1.PoolSpec{
			"app": {
				AuthQuery: &v1alpha1.AuthQuerySpec{
					User: "doorman_auth",
				},
				Users: []v1alpha1.UserSpec{
					{
						Username: "myuser",
						PoolSize: ptr.To(10),
					},
				},
			},
		},
	}

	passwords := map[string]string{
		PasswordKey("app", "auth_query", "doorman_auth"): "secret123",
		PasswordKey("app", "user", "myuser"):             "userpass",
	}

	data, err := Generate(spec, 6432, 9127, passwords, nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	var cfg wrapper.DoormanConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	pool := cfg.Pools["app"]
	if pool.AuthQuery.Password != "secret123" {
		t.Errorf("expected auth password secret123, got %q", pool.AuthQuery.Password)
	}
	if len(pool.Users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(pool.Users))
	}
	if pool.Users[0].Password != "userpass" {
		t.Errorf("expected user password userpass, got %q", pool.Users[0].Password)
	}
	if *pool.Users[0].PoolSize != 10 {
		t.Errorf("expected pool_size 10, got %d", *pool.Users[0].PoolSize)
	}
}

func TestGenerate_CustomGeneral(t *testing.T) {
	spec := &v1alpha1.PgDoormanSpec{
		General: &v1alpha1.GeneralSpec{
			WorkerThreads:   ptr.To(8),
			ConnectTimeout:  "5s",
			IdleTimeout:     "10m",
			ServerLifetime:  "15m",
			ShutdownTimeout: "30s",
		},
		Prometheus: &v1alpha1.PrometheusSpec{
			Enabled: ptr.To(false),
		},
		Pools: map[string]v1alpha1.PoolSpec{
			"app": {
				PoolMode: "session",
				AuthQuery: &v1alpha1.AuthQuerySpec{
					User: "auth",
				},
			},
		},
	}

	data, err := Generate(spec, 7000, 9200, nil, nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	var cfg wrapper.DoormanConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if cfg.General.Port != 7000 {
		t.Errorf("expected port 7000, got %d", cfg.General.Port)
	}
	if *cfg.General.WorkerThreads != 8 {
		t.Errorf("expected 8 worker threads, got %d", *cfg.General.WorkerThreads)
	}
	if cfg.General.ConnectTimeout != "5s" {
		t.Errorf("expected connect_timeout 5s, got %q", cfg.General.ConnectTimeout)
	}
	if cfg.General.IdleTimeout != "10m" {
		t.Errorf("expected idle_timeout 10m, got %q", cfg.General.IdleTimeout)
	}
	if cfg.Pools["app"].PoolMode != "session" {
		t.Errorf("expected session pool mode, got %q", cfg.Pools["app"].PoolMode)
	}
	if cfg.Prometheus != nil {
		t.Error("expected nil prometheus when disabled")
	}
}

func TestGenerate_AdminPasswordFromPasswords(t *testing.T) {
	spec := &v1alpha1.PgDoormanSpec{
		Pools: map[string]v1alpha1.PoolSpec{
			"app": {
				AuthQuery: &v1alpha1.AuthQuerySpec{User: "doorman_auth"},
			},
		},
	}

	passwords := map[string]string{
		AdminPasswordKey: "super-secret-admin",
	}

	data, err := Generate(spec, 6432, 9127, passwords, nil)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	var cfg wrapper.DoormanConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if cfg.General.AdminPassword != "super-secret-admin" {
		t.Errorf("expected admin password from passwords map, got %q", cfg.General.AdminPassword)
	}
}

func TestPasswordKey(t *testing.T) {
	got := PasswordKey("app", "auth_query", "doorman")
	if got != "app/auth_query/doorman" {
		t.Errorf("expected app/auth_query/doorman, got %q", got)
	}
}

func minimalSpec() *v1alpha1.PgDoormanSpec {
	return &v1alpha1.PgDoormanSpec{
		Pools: map[string]v1alpha1.PoolSpec{
			"app": {
				AuthQuery: &v1alpha1.AuthQuerySpec{User: "doorman_auth"},
			},
		},
	}
}

func TestGenerate_TLS(t *testing.T) {
	spec := minimalSpec()
	data, err := Generate(spec, 6432, 9127, nil, &TLSFiles{
		Certificate: "/etc/pg-doorman-tls/tls.crt",
		PrivateKey:  "/etc/pg-doorman-tls/tls.key",
	})
	if err != nil {
		t.Fatal(err)
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	general := cfg["general"].(map[string]any)
	if got := general["tls_certificate"]; got != "/etc/pg-doorman-tls/tls.crt" {
		t.Errorf("tls_certificate = %v", got)
	}
	if got := general["tls_private_key"]; got != "/etc/pg-doorman-tls/tls.key" {
		t.Errorf("tls_private_key = %v", got)
	}
}

func TestGenerate_NoTLSByDefault(t *testing.T) {
	spec := minimalSpec()
	data, err := Generate(spec, 6432, 9127, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	general := cfg["general"].(map[string]any)
	if _, ok := general["tls_certificate"]; ok {
		t.Error("tls_certificate must be omitted when TLS is not configured")
	}
}

func TestEnsureAdminPassword(t *testing.T) {
	// No spec password and no resolved secret: the random fallback must be
	// used — a fixed default would let any pod on the cluster network log in
	// to the admin console.
	spec := minimalSpec()
	passwords := EnsureAdminPassword(spec, nil, "random-fallback")
	if got := passwords[AdminPasswordKey]; got != "random-fallback" {
		t.Errorf("admin password = %q, want random fallback", got)
	}

	// Resolved secretRef password wins over the fallback.
	passwords = EnsureAdminPassword(spec, map[string]string{AdminPasswordKey: "from-secret"}, "random-fallback")
	if got := passwords[AdminPasswordKey]; got != "from-secret" {
		t.Errorf("admin password = %q, want from-secret", got)
	}

	// Explicit plaintext spec password wins over the fallback.
	spec.General = &v1alpha1.GeneralSpec{AdminPassword: "plain"}
	passwords = EnsureAdminPassword(spec, nil, "random-fallback")
	if _, ok := passwords[AdminPasswordKey]; ok {
		t.Error("fallback must not override an explicit spec adminPassword")
	}
}

func TestGenerate_NoChangeMeDefault(t *testing.T) {
	spec := minimalSpec()
	data, err := Generate(spec, 6432, 9127, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "change-me") {
		t.Error("generated config must not contain the fixed change-me admin password")
	}
}
