package configgen

import (
	"gopkg.in/yaml.v3"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/wrapper"
)

// Generate creates pg_doorman YAML config from PgDoormanSpec.
// The passwords map is keyed by "{poolName}/{type}/{username}" and contains resolved passwords.
func Generate(spec *v1alpha1.PgDoormanSpec, poolerPort, metricsPort int, passwords map[string]string) ([]byte, error) {
	applied := spec.DeepCopy()
	v1alpha1.ApplyDefaults(applied)

	cfg := wrapper.DoormanConfig{
		General: wrapper.GeneralConfig{
			Host:            "0.0.0.0",
			Port:            poolerPort,
			AdminUsername:   applied.General.AdminUsername,
			AdminPassword:   applied.General.AdminPassword,
			WorkerThreads:   *applied.General.WorkerThreads,
			MaxConnections:  *applied.General.MaxConnections,
			ConnectTimeout:  applied.General.ConnectTimeout,
			IdleTimeout:     applied.General.IdleTimeout,
			ServerLifetime:  applied.General.ServerLifetime,
			ShutdownTimeout: applied.General.ShutdownTimeout,
		},
		Pools: make(map[string]wrapper.PoolConfig, len(applied.Pools)),
	}

	if applied.Prometheus != nil && *applied.Prometheus.Enabled {
		cfg.Prometheus = &wrapper.PrometheusConfig{
			Enabled: true,
			Host:    "0.0.0.0",
			Port:    metricsPort,
		}
	}

	for poolName, pool := range applied.Pools {
		pc := wrapper.PoolConfig{
			ServerHost: "localhost",
			ServerPort: 5432,
			PoolMode:   pool.PoolMode,
		}

		if pool.AuthQuery != nil {
			aq := pool.AuthQuery
			pc.AuthQuery = &wrapper.AuthQueryConfig{
				Query:           aq.Query,
				User:            aq.User,
				Password:        passwords[PasswordKey(poolName, "auth_query", aq.User)],
				Database:        aq.Database,
				PoolSize:        *aq.PoolSize,
				DefaultPoolSize: *pool.DefaultPoolSize,
			}
		}

		for _, user := range pool.Users {
			pc.Users = append(pc.Users, wrapper.UserConfig{
				Username: user.Username,
				Password: passwords[PasswordKey(poolName, "user", user.Username)],
				PoolSize: *user.PoolSize,
			})
		}

		cfg.Pools[poolName] = pc
	}

	return yaml.Marshal(&cfg)
}

// PasswordKey returns the map key for a resolved password.
func PasswordKey(poolName, kind, username string) string {
	return poolName + "/" + kind + "/" + username
}
