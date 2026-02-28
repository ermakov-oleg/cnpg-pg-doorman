package credentials

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"

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

	// Resolve admin password from secret if configured
	if spec.General != nil && spec.General.AdminPasswordSecretRef != nil {
		val, err := ExtractSecretValue(ctx, cl, spec.General.AdminPasswordSecretRef, ns)
		if err != nil {
			return nil, fmt.Errorf("admin password: %w", err)
		}
		passwords[configgen.AdminPasswordKey] = val
	}

	return passwords, nil
}

// CollectSecretVersions computes a hash over resourceVersions of all Secrets
// referenced by the PgDoorman spec. This allows detecting secret content changes
// (e.g. password rotation) without a CR generation bump.
func CollectSecretVersions(
	ctx context.Context,
	cl client.Client,
	spec *v1alpha1.PgDoormanSpec,
	namespace string,
) (string, error) {
	seen := make(map[string]string) // secret name -> resourceVersion

	collectRef := func(ref *machineryapi.SecretKeySelector) error {
		if ref == nil {
			return nil
		}
		if _, ok := seen[ref.Name]; ok {
			return nil
		}
		secret := &corev1.Secret{}
		if err := cl.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, secret); err != nil {
			return fmt.Errorf("getting secret %s: %w", ref.Name, err)
		}
		seen[ref.Name] = secret.ResourceVersion
		return nil
	}

	for _, pool := range spec.Pools {
		if pool.AuthQuery != nil && pool.AuthQuery.PasswordSecretRef != nil {
			if err := collectRef(pool.AuthQuery.PasswordSecretRef); err != nil {
				return "", err
			}
		}
		for i := range pool.Users {
			if err := collectRef(&pool.Users[i].PasswordSecretRef); err != nil {
				return "", err
			}
		}
	}

	if spec.General != nil && spec.General.AdminPasswordSecretRef != nil {
		if err := collectRef(spec.General.AdminPasswordSecretRef); err != nil {
			return "", err
		}
	}

	if len(seen) == 0 {
		return "", nil
	}

	// Build deterministic string from sorted name:resourceVersion pairs
	pairs := make([]string, 0, len(seen))
	for name, rv := range seen {
		pairs = append(pairs, name+":"+rv)
	}
	sort.Strings(pairs)

	h := sha256.New()
	for _, p := range pairs {
		h.Write([]byte(p))
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
