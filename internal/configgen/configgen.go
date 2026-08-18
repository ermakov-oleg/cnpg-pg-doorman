package configgen

import (
	"gopkg.in/yaml.v3"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/wrapper"
)

// AdminPasswordKey is the passwords map key for the admin password resolved from SecretRef.
const AdminPasswordKey = "_admin/password"

// Generate creates pg_doorman YAML config from PgDoormanSpec.
// The passwords map is keyed by "{poolName}/{type}/{username}" and contains resolved passwords.
func Generate(spec *v1alpha1.PgDoormanSpec, poolerPort, metricsPort int, passwords map[string]string) ([]byte, error) {
	applied := spec.DeepCopy()
	v1alpha1.ApplyDefaults(applied)

	adminPassword := applied.General.AdminPassword
	if p, ok := passwords[AdminPasswordKey]; ok {
		adminPassword = p
	}

	cfg := wrapper.DoormanConfig{
		General: wrapper.GeneralConfig{
			Host:            "0.0.0.0",
			Port:            poolerPort,
			AdminUsername:   applied.General.AdminUsername,
			AdminPassword:   adminPassword,
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
		// Hardcoded for sidecar pattern: pg_doorman runs in the same pod as PostgreSQL
		pc := wrapper.PoolConfig{
			ServerHost: "localhost",
			ServerPort: 5432,
			PoolMode:   pool.PoolMode,
		}

		if pool.AuthQuery != nil {
			aq := pool.AuthQuery
			pc.AuthQuery = &wrapper.AuthQueryConfig{
				Query:    aq.Query,
				User:     aq.User,
				Password: passwords[PasswordKey(poolName, "auth_query", aq.User)],
				Database: aq.Database,
				Workers:  *aq.PoolSize,
				PoolSize: *pool.DefaultPoolSize,
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
