package v1alpha1

import "k8s.io/utils/ptr"

const (
	DefaultPoolMode        = "transaction"
	DefaultDefaultPoolSize = 40
	DefaultUserPoolSize    = 20

	DefaultAuthQueryQuery    = "SELECT * FROM public.doorman_auth_query($1)"
	DefaultAuthQueryDatabase = "postgres"
	DefaultAuthQueryPoolSize = 2

	DefaultMaxConnections  = 8192
	DefaultWorkerThreads   = 4
	DefaultConnectTimeout  = "3s"
	DefaultIdleTimeout     = "5m"
	DefaultServerLifetime  = "5m"
	DefaultShutdownTimeout = "10s"
	DefaultAdminUsername   = "admin"
	DefaultAdminPassword   = "change-me"
)

// ApplyDefaults fills in default values for unset fields.
func ApplyDefaults(spec *PgDoormanSpec) {
	if spec.General == nil {
		spec.General = &GeneralSpec{}
	}

	g := spec.General
	if g.MaxConnections == nil {
		g.MaxConnections = ptr.To(DefaultMaxConnections)
	}
	if g.WorkerThreads == nil {
		g.WorkerThreads = ptr.To(DefaultWorkerThreads)
	}
	if g.ConnectTimeout == "" {
		g.ConnectTimeout = DefaultConnectTimeout
	}
	if g.IdleTimeout == "" {
		g.IdleTimeout = DefaultIdleTimeout
	}
	if g.ServerLifetime == "" {
		g.ServerLifetime = DefaultServerLifetime
	}
	if g.ShutdownTimeout == "" {
		g.ShutdownTimeout = DefaultShutdownTimeout
	}
	if g.AdminUsername == "" {
		g.AdminUsername = DefaultAdminUsername
	}
	if g.AdminPassword == "" {
		g.AdminPassword = DefaultAdminPassword
	}

	if spec.Prometheus == nil {
		spec.Prometheus = &PrometheusSpec{}
	}
	if spec.Prometheus.Enabled == nil {
		spec.Prometheus.Enabled = ptr.To(true)
	}

	for name, pool := range spec.Pools {
		if pool.PoolMode == "" {
			pool.PoolMode = DefaultPoolMode
		}
		if pool.DefaultPoolSize == nil {
			pool.DefaultPoolSize = ptr.To(DefaultDefaultPoolSize)
		}
		if pool.AuthQuery != nil {
			if pool.AuthQuery.Query == "" {
				pool.AuthQuery.Query = DefaultAuthQueryQuery
			}
			if pool.AuthQuery.Database == "" {
				pool.AuthQuery.Database = DefaultAuthQueryDatabase
			}
			if pool.AuthQuery.PoolSize == nil {
				pool.AuthQuery.PoolSize = ptr.To(DefaultAuthQueryPoolSize)
			}
		}
		for i := range pool.Users {
			if pool.Users[i].PoolSize == nil {
				pool.Users[i].PoolSize = ptr.To(DefaultUserPoolSize)
			}
		}
		spec.Pools[name] = pool
	}
}
