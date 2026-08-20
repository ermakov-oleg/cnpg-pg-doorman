package wrapper

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ConfigTester validates a config file, typically with the real pg_doorman
// binary: the Go-side structural check cannot know which values pg_doorman
// itself will reject.
type ConfigTester func(ctx context.Context, path string) error

const binaryTestTimeout = 10 * time.Second

// CandidateSuffix names the not-yet-accepted config next to the runtime file.
// It must keep a .yaml extension: pg_doorman detects the config format from
// the file extension and would parse anything else as TOML.
const CandidateSuffix = ".next.yaml"

// ValidateConfigFlag makes pg_doorman parse the config and exit. Such a run is
// the same binary as the pooler, so the successor scan has to tell them apart.
const ValidateConfigFlag = "--test-config"

// NewBinaryConfigTester returns a ConfigTester running `binary <path> --test-config`.
func NewBinaryConfigTester(binary string) ConfigTester {
	return func(ctx context.Context, path string) error {
		ctx, cancel := context.WithTimeout(ctx, binaryTestTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, binary, path, ValidateConfigFlag).CombinedOutput() //nolint:gosec // fixed binary, path is our candidate file
		if err != nil {
			return fmt.Errorf("pg_doorman --test-config failed: %w: %s", err, out)
		}
		return nil
	}
}

// DoormanConfig is a minimal pg_doorman config structure for validation.
type DoormanConfig struct {
	General    GeneralConfig         `yaml:"general"`
	Pools      map[string]PoolConfig `yaml:"pools"`
	Prometheus *PrometheusConfig     `yaml:"prometheus,omitempty"`
}

type GeneralConfig struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	TLSCertificate  string `yaml:"tls_certificate,omitempty"`
	TLSPrivateKey   string `yaml:"tls_private_key,omitempty"`
	AdminUsername   string `yaml:"admin_username"`
	AdminPassword   string `yaml:"admin_password"`
	WorkerThreads   *int   `yaml:"worker_threads,omitempty"`
	MaxConnections  *int   `yaml:"max_connections,omitempty"`
	ConnectTimeout  string `yaml:"connect_timeout,omitempty"`
	IdleTimeout     string `yaml:"idle_timeout,omitempty"`
	ServerLifetime  string `yaml:"server_lifetime,omitempty"`
	ShutdownTimeout string `yaml:"shutdown_timeout,omitempty"`
}

type PoolConfig struct {
	ServerHost string           `yaml:"server_host"`
	ServerPort int              `yaml:"server_port"`
	PoolMode   string           `yaml:"pool_mode"`
	Users      []UserConfig     `yaml:"users,omitempty"`
	AuthQuery  *AuthQueryConfig `yaml:"auth_query,omitempty"`
}

type UserConfig struct {
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	PoolSize *int   `yaml:"pool_size,omitempty"`
}

type AuthQueryConfig struct {
	Query    string `yaml:"query"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	// workers = executor connections, pool_size = per-user data pool
	Workers         *int   `yaml:"workers,omitempty"`
	PoolSize        *int   `yaml:"pool_size,omitempty"`
	CacheTTL        string `yaml:"cache_ttl,omitempty"`
	CacheFailureTTL string `yaml:"cache_failure_ttl,omitempty"`
	MinInterval     string `yaml:"min_interval,omitempty"`
}

type PrometheusConfig struct {
	Enabled bool   `yaml:"enabled"`
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
}

// durationPattern mirrors pg_doorman's duration parser: a plain number
// (milliseconds) or an integer with a case-insensitive ms/s/m/h/d suffix.
var durationPattern = regexp.MustCompile(`^[0-9]+(ms|s|m|h|d)?$`)

func validateDuration(field, value string) error {
	if value == "" {
		return nil
	}
	if !durationPattern.MatchString(strings.ToLower(strings.TrimSpace(value))) {
		return fmt.Errorf("%s: invalid duration %q, expected a number or a value with ms/s/m/h/d suffix", field, value)
	}
	return nil
}

func validatePositive(field string, value *int) error {
	if value != nil && *value < 1 {
		return fmt.Errorf("%s: must be >= 1, got %d", field, *value)
	}
	return nil
}

func ValidateConfigBytes(data []byte) (*DoormanConfig, error) {
	var cfg DoormanConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}

	if len(cfg.Pools) == 0 {
		return nil, fmt.Errorf("at least one pool must be defined")
	}

	if err := validateGeneral(&cfg.General); err != nil {
		return nil, err
	}

	for name, pool := range cfg.Pools {
		if err := validatePool(name, &pool); err != nil {
			return nil, err
		}
	}

	return &cfg, nil
}

func validateGeneral(g *GeneralConfig) error {
	if err := validatePositive("general.worker_threads", g.WorkerThreads); err != nil {
		return err
	}
	if err := validatePositive("general.max_connections", g.MaxConnections); err != nil {
		return err
	}

	durations := map[string]string{
		"general.connect_timeout":  g.ConnectTimeout,
		"general.idle_timeout":     g.IdleTimeout,
		"general.server_lifetime":  g.ServerLifetime,
		"general.shutdown_timeout": g.ShutdownTimeout,
	}
	for field, value := range durations {
		if err := validateDuration(field, value); err != nil {
			return err
		}
	}
	return nil
}

func validatePool(name string, pool *PoolConfig) error {
	if pool.PoolMode != "" && pool.PoolMode != "session" && pool.PoolMode != "transaction" {
		return fmt.Errorf("pool %q: pool_mode must be \"session\" or \"transaction\", got %q", name, pool.PoolMode)
	}
	if len(pool.Users) == 0 && pool.AuthQuery == nil {
		return fmt.Errorf("pool %q: must have users or auth_query", name)
	}
	if pool.AuthQuery != nil {
		if err := validateAuthQuery(name, pool.AuthQuery); err != nil {
			return err
		}
	}
	for i, user := range pool.Users {
		if user.Username == "" {
			return fmt.Errorf("pool %q: users[%d].username is required", name, i)
		}
		if user.Password == "" {
			return fmt.Errorf("pool %q: users[%d].password is required", name, i)
		}
		if err := validatePositive(fmt.Sprintf("pool %q: users[%d].pool_size", name, i), user.PoolSize); err != nil {
			return err
		}
	}
	return nil
}

func validateAuthQuery(name string, aq *AuthQueryConfig) error {
	if aq.Query == "" {
		return fmt.Errorf("pool %q: auth_query.query is required", name)
	}
	if aq.User == "" {
		return fmt.Errorf("pool %q: auth_query.user is required", name)
	}
	if err := validatePositive(fmt.Sprintf("pool %q: auth_query.workers", name), aq.Workers); err != nil {
		return err
	}
	if err := validatePositive(fmt.Sprintf("pool %q: auth_query.pool_size", name), aq.PoolSize); err != nil {
		return err
	}

	durations := map[string]string{
		fmt.Sprintf("pool %q: auth_query.cache_ttl", name):         aq.CacheTTL,
		fmt.Sprintf("pool %q: auth_query.cache_failure_ttl", name): aq.CacheFailureTTL,
		fmt.Sprintf("pool %q: auth_query.min_interval", name):      aq.MinInterval,
	}
	for field, value := range durations {
		if err := validateDuration(field, value); err != nil {
			return err
		}
	}
	return nil
}

// AtomicWrite writes data via temp file + fsync + rename.
func AtomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	// 0600: the config carries plaintext passwords.
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // path is derived from our own constant
	if err != nil {
		return err
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	return os.Rename(tmp, path)
}
