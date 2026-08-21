package specs

import (
	"fmt"

	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
)

// GeneratedAuthSecretName returns the conventional name of the generated
// auth_query password Secret for a cluster.
func GeneratedAuthSecretName(clusterName string) string {
	return clusterName + "-doorman-auth"
}

// DefaultAuthSecretRefs returns a copy of the spec where every authQuery
// without a passwordSecretRef points at the generated per-cluster Secret,
// plus the auth user that Secret must carry ("" when nothing was defaulted).
// Pools omitting the ref must agree on the user: the generated Secret is
// kubernetes.io/basic-auth and holds a single username.
func DefaultAuthSecretRefs(spec *v1alpha1.PgDoormanSpec) (*v1alpha1.PgDoormanSpec, string, error) {
	normalized := spec.DeepCopy()
	user := ""

	for name, pool := range normalized.Pools {
		if pool.AuthQuery == nil || pool.AuthQuery.PasswordSecretRef != nil {
			continue
		}
		if user != "" && pool.AuthQuery.User != user {
			return nil, "", fmt.Errorf(
				"pools omitting authQuery.passwordSecretRef must use the same user, got %q and %q",
				user, pool.AuthQuery.User)
		}
		user = pool.AuthQuery.User
		pool.AuthQuery.PasswordSecretRef = &machineryapi.SecretKeySelector{
			LocalObjectReference: machineryapi.LocalObjectReference{
				Name: GeneratedAuthSecretName(normalized.ClusterRef.Name),
			},
			Key: "password",
		}
		normalized.Pools[name] = pool
	}

	return normalized, user, nil
}
