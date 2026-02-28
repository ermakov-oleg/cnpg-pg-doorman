package pooler

import (
	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{
			"pg_doorman.yaml": `
general:
  host: "0.0.0.0"
  port: 6432
  connect_timeout: "3s"
  idle_timeout: "5m"
  server_lifetime: "5m"
  shutdown_timeout: "10s"
  worker_threads: 2
  admin_username: "admin"
  admin_password: "admin"

prometheus:
  enabled: true
  host: "0.0.0.0"
  port: 9127

pools:
  app:
    server_host: "127.0.0.1"
    server_port: 5432
    pool_mode: "transaction"

    auth_query:
      query: "SELECT * FROM public.doorman_auth_query($1)"
      user: "doorman_auth"
      password: ""
      database: "app"
      pool_size: 2
      default_pool_size: 20
      cache_ttl: "1h"
`,
		},
	}
}

// newConfigMapWithPoolMode creates a ConfigMap with a specified pool_mode.
func newConfigMapWithPoolMode(namespace, name, poolMode string) *corev1.ConfigMap {
	cm := newConfigMap(namespace, name)
	cm.Data["pg_doorman.yaml"] = `
general:
  host: "0.0.0.0"
  port: 6432
  connect_timeout: "3s"
  idle_timeout: "5m"
  server_lifetime: "5m"
  shutdown_timeout: "10s"
  worker_threads: 2
  admin_username: "admin"
  admin_password: "admin"

prometheus:
  enabled: true
  host: "0.0.0.0"
  port: 9127

pools:
  app:
    server_host: "127.0.0.1"
    server_port: 5432
    pool_mode: "` + poolMode + `"

    auth_query:
      query: "SELECT * FROM public.doorman_auth_query($1)"
      user: "doorman_auth"
      password: ""
      database: "app"
      pool_size: 2
      default_pool_size: 20
      cache_ttl: "1h"
`
	return cm
}

// newInvalidConfigMap creates a ConfigMap with invalid pg_doorman config (no pools).
func newInvalidConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{
			"pg_doorman.yaml": `
general:
  host: "0.0.0.0"
  port: 6432
`,
		},
	}
}

func newCluster(namespace, name, configMapName string) *cnpgv1.Cluster {
	return &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: cnpgv1.ClusterSpec{
			Instances: 1,
			Plugins: []cnpgv1.PluginConfiguration{
				{
					Name: "pg-doorman.cnpg.io",
					Parameters: map[string]string{
						"poolerPort":    "6432",
						"metricsPort":   "9127",
						"configMapName": configMapName,
					},
				},
			},
			PostgresConfiguration: cnpgv1.PostgresConfiguration{
				PgHBA: []string{
					"host all doorman_auth 127.0.0.1/32 trust",
					"host all doorman_auth ::1/128 trust",
				},
			},
			Bootstrap: &cnpgv1.BootstrapConfiguration{
				InitDB: &cnpgv1.BootstrapInitDB{
					Database: "app",
					Owner:    "app",
					PostInitSQL: []string{
						"CREATE ROLE doorman_auth WITH LOGIN NOINHERIT",
					},
					PostInitApplicationSQL: []string{
						"CREATE OR REPLACE FUNCTION public.doorman_auth_query(username TEXT) RETURNS TABLE (usename name, passwd text) SECURITY DEFINER SET search_path = pg_catalog LANGUAGE SQL AS 'SELECT usename, passwd FROM pg_shadow WHERE usename = $1'",
						"GRANT EXECUTE ON FUNCTION public.doorman_auth_query(TEXT) TO doorman_auth",
					},
				},
			},
			StorageConfiguration: cnpgv1.StorageConfiguration{
				Size: "1Gi",
			},
		},
	}
}

// newClusterWithMissingConfigMap creates a Cluster referencing a non-existent ConfigMap.
func newClusterWithMissingConfigMap(namespace, name string) *cnpgv1.Cluster {
	return newCluster(namespace, name, "non-existent-configmap")
}
