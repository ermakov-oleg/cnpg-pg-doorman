//go:build e2e

package pooler

import (
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	pgdoormanv1alpha1 "github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/specs"
)

// e2eAdminPassword is provided via a Secret referenced from every fixture CR:
// without it the wrapper generates a random per-pod admin password the tests
// cannot know. The plaintext adminPassword API field is deliberately gone.
const (
	e2eAdminPassword       = "e2e-admin-password"
	e2eAdminPasswordSecret = "e2e-admin-password"
)

// newAdminPasswordSecret creates the Secret backing adminPasswordSecretRef,
// labeled for the cluster (the render controller rejects unlabeled secrets).
func newAdminPasswordSecret(namespace, clusterName string) *corev1.Secret {
	return newPasswordSecret(namespace, e2eAdminPasswordSecret, clusterName, e2eAdminPassword)
}

func newPgDoorman(namespace, name, clusterName string) *pgdoormanv1alpha1.PgDoorman {
	return &pgdoormanv1alpha1.PgDoorman{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: pgdoormanv1alpha1.PgDoormanSpec{
			ClusterRef: machineryapi.LocalObjectReference{Name: clusterName},
			General: &pgdoormanv1alpha1.GeneralSpec{
				WorkerThreads: ptr.To(2),
				AdminPasswordSecretRef: &machineryapi.SecretKeySelector{
					LocalObjectReference: machineryapi.LocalObjectReference{
						Name: e2eAdminPasswordSecret,
					},
					Key: "password",
				},
			},
			Pools: map[string]pgdoormanv1alpha1.PoolSpec{
				"app": {
					AuthQuery: &pgdoormanv1alpha1.AuthQuerySpec{
						User:     "doorman_auth",
						Database: "app",
					},
				},
			},
		},
	}
}

func newCluster(namespace, name, configName string) *cnpgv1.Cluster {
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
						"poolerPort":  "6432",
						"metricsPort": "9127",
						"configName":  configName,
					},
				},
			},
			PostgresConfiguration: cnpgv1.PostgresConfiguration{
				PgHBA: []string{
					"host all doorman_auth 127.0.0.1/32 scram-sha-256",
					"host all doorman_auth ::1/128 scram-sha-256",
				},
			},
			// The doorman_auth password comes from the Secret the plugin
			// generates (no passwordSecretRef in the fixture CR), exercising
			// the generation path end-to-end in every scenario.
			Managed: &cnpgv1.ManagedConfiguration{
				Roles: []cnpgv1.RoleConfiguration{
					{
						Name:    "doorman_auth",
						Login:   true,
						Inherit: ptr.To(false),
						PasswordSecret: &cnpgv1.LocalObjectReference{
							Name: specs.GeneratedAuthSecretName(name),
						},
					},
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

// newClusterWithInstances creates a Cluster with the given number of instances.
func newClusterWithInstances(namespace, name, configName string, instances int) *cnpgv1.Cluster {
	c := newCluster(namespace, name, configName)
	c.Spec.Instances = instances
	return c
}

// newClusterWithInPlaceUpgrades creates a Cluster that opts into in-place
// pg_doorman binary upgrades via the inPlaceUpgrades plugin parameter.
func newClusterWithInPlaceUpgrades(namespace, name, configName string) *cnpgv1.Cluster {
	c := newCluster(namespace, name, configName)
	c.Spec.Plugins[0].Parameters["inPlaceUpgrades"] = "true"
	return c
}

// newClusterWithMissingConfig creates a Cluster referencing a non-existent PgDoorman CR.
func newClusterWithMissingConfig(namespace, name string) *cnpgv1.Cluster {
	return newCluster(namespace, name, "non-existent-config")
}

// newClusterWithoutPlugin creates a Cluster with no pg-doorman plugin.
func newClusterWithoutPlugin(namespace, name string) *cnpgv1.Cluster {
	return &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: cnpgv1.ClusterSpec{
			Instances: 1,
			Bootstrap: &cnpgv1.BootstrapConfiguration{
				InitDB: &cnpgv1.BootstrapInitDB{
					Database: "app",
					Owner:    "app",
				},
			},
			StorageConfiguration: cnpgv1.StorageConfiguration{
				Size: "1Gi",
			},
		},
	}
}

// newClusterWithInvalidParams creates a Cluster with invalid plugin parameters.
func newClusterWithInvalidParams(namespace, name, configName string) *cnpgv1.Cluster {
	c := newCluster(namespace, name, configName)
	c.Spec.Plugins[0].Parameters["poolerPort"] = "not-a-number"
	return c
}

// newPasswordSecret creates a Secret with a password key, labeled as owned by
// the cluster: the render controller only accepts labeled secrets.
func newPasswordSecret(namespace, name, clusterName, password string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"cnpg.io/cluster": clusterName},
		},
		StringData: map[string]string{
			"password": password,
		},
	}
}

// newPgDoormanWithSecretRef creates a PgDoorman CR with admin password from a Secret.
func newPgDoormanWithSecretRef(namespace, name, clusterName, secretName string) *pgdoormanv1alpha1.PgDoorman {
	cr := newPgDoorman(namespace, name, clusterName)
	cr.Spec.General.AdminPasswordSecretRef = &machineryapi.SecretKeySelector{
		LocalObjectReference: machineryapi.LocalObjectReference{
			Name: secretName,
		},
		Key: "password",
	}
	return cr
}
