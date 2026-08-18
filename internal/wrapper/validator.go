package wrapper

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// DoormanConfig is a minimal pg_doorman config structure for validation.
type DoormanConfig struct {
	General    GeneralConfig         `yaml:"general"`
	Pools      map[string]PoolConfig `yaml:"pools"`
	Prometheus *PrometheusConfig     `yaml:"prometheus,omitempty"`
}

type GeneralConfig struct {
	Host            string `yaml:"host"`
	Port            int    `yaml:"port"`
	AdminUsername   string `yaml:"admin_username"`
	AdminPassword   string `yaml:"admin_password"`
	WorkerThreads   int    `yaml:"worker_threads"`
	MaxConnections  int    `yaml:"max_connections"`
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
	PoolSize int    `yaml:"pool_size"`
}

type AuthQueryConfig struct {
	Query    string `yaml:"query"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
	// workers = executor connections, pool_size = per-user data pool
	Workers         int    `yaml:"workers"`
	PoolSize        int    `yaml:"pool_size"`
	CacheTTL        string `yaml:"cache_ttl,omitempty"`
	CacheFailureTTL string `yaml:"cache_failure_ttl,omitempty"`
	MinInterval     string `yaml:"min_interval,omitempty"`
}

type PrometheusConfig struct {
	Enabled bool   `yaml:"enabled"`
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
}

func ValidateConfigBytes(data []byte) (*DoormanConfig, error) {
	var cfg DoormanConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}

	if len(cfg.Pools) == 0 {
		return nil, fmt.Errorf("at least one pool must be defined")
	}

	for name, pool := range cfg.Pools {
		if len(pool.Users) == 0 && pool.AuthQuery == nil {
			return nil, fmt.Errorf("pool %q: must have users or auth_query", name)
		}
		if pool.AuthQuery != nil {
			if pool.AuthQuery.Query == "" {
				return nil, fmt.Errorf("pool %q: auth_query.query is required", name)
			}
			if pool.AuthQuery.User == "" {
				return nil, fmt.Errorf("pool %q: auth_query.user is required", name)
			}
		}
		for i, user := range pool.Users {
			if user.Username == "" {
				return nil, fmt.Errorf("pool %q: users[%d].username is required", name, i)
			}
			if user.Password == "" {
				return nil, fmt.Errorf("pool %q: users[%d].password is required", name, i)
			}
		}
	}

	return &cfg, nil
}

// AtomicWrite writes data via temp file + fsync + rename.
func AtomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
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
