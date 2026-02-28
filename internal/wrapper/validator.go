package wrapper

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

// DoormanConfig — минимальная структура конфига pg_doorman для валидации.
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
	Query           string `yaml:"query"`
	User            string `yaml:"user"`
	Password        string `yaml:"password"`
	Database        string `yaml:"database"`
	PoolSize        int    `yaml:"pool_size"`
	DefaultPoolSize int    `yaml:"default_pool_size"`
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

func ValidateConfigFile(path string) (*DoormanConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	return ValidateConfigBytes(data)
}

// ValidateAndCopyConfig валидирует конфиг и делает atomic copy в destination.
func ValidateAndCopyConfig(src, dst string, logger *slog.Logger) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read config: %w", err)
	}

	if _, err := ValidateConfigBytes(data); err != nil {
		return err
	}

	if err := AtomicWrite(dst, data); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	logger.Info("config validated and copied", "src", src, "dst", dst)
	return nil
}

// AtomicWrite записывает данные через temp file + fsync + rename.
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

// FileHash возвращает SHA256 хеш файла.
func FileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close() //nolint:errcheck // read-only file

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
