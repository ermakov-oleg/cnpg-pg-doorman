package specs

import (
	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
)

// CollectSecretNames extracts all Secret names referenced in PgDoormanSpec.
func CollectSecretNames(spec *v1alpha1.PgDoormanSpec) []string {
	seen := make(map[string]struct{})
	var result []string

	add := func(name string) {
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			result = append(result, name)
		}
	}

	for _, pool := range spec.Pools {
		if pool.AuthQuery != nil && pool.AuthQuery.PasswordSecretRef != nil {
			add(pool.AuthQuery.PasswordSecretRef.Name)
		}
		for _, user := range pool.Users {
			add(user.PasswordSecretRef.Name)
		}
	}

	if spec.General != nil && spec.General.AdminPasswordSecretRef != nil {
		add(spec.General.AdminPasswordSecretRef.Name)
	}

	return result
}
