package specs

import (
	"fmt"
	"sort"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
)

// BuildRole creates a Role for the cluster ServiceAccount to read PgDoorman CR and referenced Secrets.
func BuildRole(
	cluster *cnpgv1.Cluster,
	pgDoorman *v1alpha1.PgDoorman,
) *rbacv1.Role {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      GetRBACName(cluster.Name),
		},
		Rules: []rbacv1.PolicyRule{},
	}

	// CRD access rule. Only get: the wrapper polls by name, and list/watch
	// do not work with resourceNames-scoped RBAC anyway.
	role.Rules = append(role.Rules, rbacv1.PolicyRule{
		APIGroups:     []string{"pg-doorman.cnpg.io"},
		Verbs:         []string{"get"},
		Resources:     []string{"pgdoormen"},
		ResourceNames: []string{pgDoorman.Name},
	})

	// Secret access rule
	secretNames := CollectSecretNames(&pgDoorman.Spec)
	sort.Strings(secretNames)
	if len(secretNames) > 0 {
		role.Rules = append(role.Rules, rbacv1.PolicyRule{
			APIGroups:     []string{""},
			Resources:     []string{"secrets"},
			Verbs:         []string{"get"},
			ResourceNames: secretNames,
		})
	}

	return role
}

// BuildRoleBinding creates a RoleBinding linking the cluster ServiceAccount to the Role.
func BuildRoleBinding(cluster *cnpgv1.Cluster) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      GetRBACName(cluster.Name),
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				APIGroup:  "",
				Name:      cluster.Name,
				Namespace: cluster.Namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     GetRBACName(cluster.Name),
		},
	}
}

// GetRBACName returns the RBAC entity name for the pg-doorman plugin.
func GetRBACName(clusterName string) string {
	return fmt.Sprintf("%s-pg-doorman", clusterName)
}
