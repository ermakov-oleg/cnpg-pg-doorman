package credentials

import (
	"context"
	"fmt"

	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/configgen"
)

// ExtractSecretValue reads a value from a Secret referenced by SecretKeySelector.
func ExtractSecretValue(
	ctx context.Context,
	cl client.Client,
	ref *machineryapi.SecretKeySelector,
	ns string,
) (string, error) {
	secret := &corev1.Secret{}
	if err := cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: ref.Name}, secret); err != nil {
		return "", fmt.Errorf("getting secret %s: %w", ref.Name, err)
	}

	value, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("missing key %s in secret %s", ref.Key, ref.Name)
	}
	return string(value), nil
}

// ResolvePasswords resolves all SecretRef passwords from the spec into a password map.
func ResolvePasswords(
	ctx context.Context,
	cl client.Client,
	ns string,
	spec *v1alpha1.PgDoormanSpec,
) (map[string]string, error) {
	passwords := make(map[string]string)

	for poolName, pool := range spec.Pools {
		if pool.AuthQuery != nil && pool.AuthQuery.PasswordSecretRef != nil {
			val, err := ExtractSecretValue(ctx, cl, pool.AuthQuery.PasswordSecretRef, ns)
			if err != nil {
				return nil, fmt.Errorf("pool %q auth_query password: %w", poolName, err)
			}
			passwords[configgen.PasswordKey(poolName, "auth_query", pool.AuthQuery.User)] = val
		}

		for _, user := range pool.Users {
			val, err := ExtractSecretValue(ctx, cl, &user.PasswordSecretRef, ns)
			if err != nil {
				return nil, fmt.Errorf("pool %q user %q password: %w", poolName, user.Username, err)
			}
			passwords[configgen.PasswordKey(poolName, "user", user.Username)] = val
		}
	}

	return passwords, nil
}
