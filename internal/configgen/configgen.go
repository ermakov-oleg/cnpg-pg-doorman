package configgen

import (
	"gopkg.in/yaml.v3"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/wrapper"
)

// AdminPasswordKey is the passwords map key for the admin password resolved from SecretRef.
const AdminPasswordKey = "_admin/password"

// TLSFiles points to the certificate pair pg_doorman terminates client TLS with.
type TLSFiles struct {
	Certificate string
	PrivateKey  string
}

// Generate creates pg_doorman YAML config from PgDoormanSpec.
// The passwords map is keyed by "{poolName}/{type}/{username}" and contains resolved passwords.
func Generate(spec *v1alpha1.PgDoormanSpec, poolerPort, metricsPort int, passwords map[string]string, tls *TLSFiles) ([]byte, error) {
	applied := spec.DeepCopy()
	v1alpha1.ApplyDefaults(applied)

	adminPassword := passwords[AdminPasswordKey]

	cfg := wrapper.DoormanConfig{
		General: wrapper.GeneralConfig{
			Host:            "0.0.0.0",
			Port:            poolerPort,
			AdminUsername:   applied.General.AdminUsername,
			AdminPassword:   adminPassword,
			WorkerThreads:   applied.General.WorkerThreads,
			MaxConnections:  applied.General.MaxConnections,
			ConnectTimeout:  applied.General.ConnectTimeout,
			IdleTimeout:     applied.General.IdleTimeout,
			ServerLifetime:  applied.General.ServerLifetime,
			ShutdownTimeout: applied.General.ShutdownTimeout,
		},
		Pools: make(map[string]wrapper.PoolConfig, len(applied.Pools)),
	}

	if tls != nil {
		cfg.General.TLSCertificate = tls.Certificate
		cfg.General.TLSPrivateKey = tls.PrivateKey
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
				Workers:  aq.PoolSize,
				PoolSize: pool.DefaultPoolSize,
			}
		}

		for _, user := range pool.Users {
			pc.Users = append(pc.Users, wrapper.UserConfig{
				Username: user.Username,
				Password: passwords[PasswordKey(poolName, "user", user.Username)],
				PoolSize: user.PoolSize,
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

// EnsureAdminPassword injects fallback as the admin password when the spec
// provides no adminPasswordSecretRef (already resolved into passwords).
// A fixed default would let any pod on the cluster network log in to the
// admin console on the pooler port.
func EnsureAdminPassword(passwords map[string]string, fallback string) map[string]string {
	if _, ok := passwords[AdminPasswordKey]; ok {
		return passwords
	}
	if passwords == nil {
		passwords = make(map[string]string, 1)
	}
	passwords[AdminPasswordKey] = fallback
	return passwords
}
