package specs

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
)

func TestBuildRole_CRDRuleOnly(t *testing.T) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: "default"},
	}
	pgDoorman := &v1alpha1.PgDoorman{
		ObjectMeta: metav1.ObjectMeta{Name: "my-config"},
		Spec: v1alpha1.PgDoormanSpec{
			Pools: map[string]v1alpha1.PoolSpec{
				"app": {AuthQuery: &v1alpha1.AuthQuerySpec{User: "auth"}},
			},
		},
	}

	role := BuildRole(cluster, pgDoorman)

	if role.Name != "my-cluster-pg-doorman" {
		t.Errorf("expected role name my-cluster-pg-doorman, got %q", role.Name)
	}
	if role.Namespace != "default" {
		t.Errorf("expected namespace default, got %q", role.Namespace)
	}
	// Only CRD rule, no secrets rule (no secret refs)
	if len(role.Rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(role.Rules))
	}
	if role.Rules[0].Resources[0] != "pgdoormen" {
		t.Errorf("expected pgdoormen resource, got %q", role.Rules[0].Resources[0])
	}
}

func TestBuildRole_WithSecrets(t *testing.T) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: "default"},
	}
	pgDoorman := &v1alpha1.PgDoorman{
		ObjectMeta: metav1.ObjectMeta{Name: "my-config"},
		Spec: v1alpha1.PgDoormanSpec{
			Pools: map[string]v1alpha1.PoolSpec{
				"app": {
					AuthQuery: &v1alpha1.AuthQuerySpec{
						User: "auth",
						PasswordSecretRef: &machineryapi.SecretKeySelector{
							LocalObjectReference: machineryapi.LocalObjectReference{Name: "auth-secret"},
							Key:                  "password",
						},
					},
				},
			},
		},
	}

	role := BuildRole(cluster, pgDoorman)

	if len(role.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(role.Rules))
	}

	secretRule := role.Rules[1]
	if secretRule.Resources[0] != "secrets" {
		t.Errorf("expected secrets resource, got %q", secretRule.Resources[0])
	}
	if len(secretRule.Verbs) != 1 || secretRule.Verbs[0] != "get" {
		t.Errorf("expected [get] verbs, got %v", secretRule.Verbs)
	}
	if len(secretRule.ResourceNames) != 1 || secretRule.ResourceNames[0] != "auth-secret" {
		t.Errorf("expected [auth-secret], got %v", secretRule.ResourceNames)
	}
}

func TestBuildRoleBinding(t *testing.T) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "my-cluster", Namespace: "default"},
	}

	rb := BuildRoleBinding(cluster)

	if rb.Name != "my-cluster-pg-doorman" {
		t.Errorf("expected name my-cluster-pg-doorman, got %q", rb.Name)
	}
	if len(rb.Subjects) != 1 {
		t.Fatalf("expected 1 subject, got %d", len(rb.Subjects))
	}
	if rb.Subjects[0].Name != "my-cluster" {
		t.Errorf("expected subject name my-cluster, got %q", rb.Subjects[0].Name)
	}
	if rb.RoleRef.Name != "my-cluster-pg-doorman" {
		t.Errorf("expected roleRef name my-cluster-pg-doorman, got %q", rb.RoleRef.Name)
	}
}

func TestGetRBACName(t *testing.T) {
	if got := GetRBACName("foo"); got != "foo-pg-doorman" {
		t.Errorf("expected foo-pg-doorman, got %q", got)
	}
}
