package specs

import (
	"testing"

	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
)

func specWithPools(pools map[string]v1alpha1.PoolSpec) *v1alpha1.PgDoormanSpec {
	return &v1alpha1.PgDoormanSpec{
		ClusterRef: machineryapi.LocalObjectReference{Name: "pg"},
		Pools:      pools,
	}
}

func TestDefaultAuthSecretRefsFillsMissingRef(t *testing.T) {
	spec := specWithPools(map[string]v1alpha1.PoolSpec{
		"app": {AuthQuery: &v1alpha1.AuthQuerySpec{User: "doorman_auth"}},
	})

	normalized, user, err := DefaultAuthSecretRefs(spec)
	if err != nil {
		t.Fatalf("DefaultAuthSecretRefs: %v", err)
	}

	ref := normalized.Pools["app"].AuthQuery.PasswordSecretRef
	if ref == nil {
		t.Fatal("missing passwordSecretRef must be defaulted")
	}
	if ref.Name != GeneratedAuthSecretName("pg") || ref.Key != "password" {
		t.Errorf("defaulted ref = %s/%s, want %s/password", ref.Name, ref.Key, GeneratedAuthSecretName("pg"))
	}
	if user != "doorman_auth" {
		t.Errorf("generated auth user = %q, want doorman_auth", user)
	}
	if spec.Pools["app"].AuthQuery.PasswordSecretRef != nil {
		t.Error("original spec must not be mutated")
	}
}

func TestDefaultAuthSecretRefsKeepsExplicitRef(t *testing.T) {
	explicit := &machineryapi.SecretKeySelector{
		LocalObjectReference: machineryapi.LocalObjectReference{Name: "my-secret"},
		Key:                  "pass",
	}
	spec := specWithPools(map[string]v1alpha1.PoolSpec{
		"app": {AuthQuery: &v1alpha1.AuthQuerySpec{User: "doorman_auth", PasswordSecretRef: explicit}},
	})

	normalized, user, err := DefaultAuthSecretRefs(spec)
	if err != nil {
		t.Fatalf("DefaultAuthSecretRefs: %v", err)
	}

	ref := normalized.Pools["app"].AuthQuery.PasswordSecretRef
	if ref == nil || ref.Name != "my-secret" || ref.Key != "pass" {
		t.Errorf("explicit ref must stay untouched, got %+v", ref)
	}
	if user != "" {
		t.Errorf("nothing generated: auth user must be empty, got %q", user)
	}
}

func TestDefaultAuthSecretRefsSharesSecretAcrossPools(t *testing.T) {
	spec := specWithPools(map[string]v1alpha1.PoolSpec{
		"app":     {AuthQuery: &v1alpha1.AuthQuerySpec{User: "doorman_auth"}},
		"reports": {AuthQuery: &v1alpha1.AuthQuerySpec{User: "doorman_auth"}},
	})

	normalized, user, err := DefaultAuthSecretRefs(spec)
	if err != nil {
		t.Fatalf("DefaultAuthSecretRefs: %v", err)
	}

	for name, pool := range normalized.Pools {
		ref := pool.AuthQuery.PasswordSecretRef
		if ref == nil || ref.Name != GeneratedAuthSecretName("pg") {
			t.Errorf("pool %q: ref = %+v, want generated secret", name, ref)
		}
	}
	if user != "doorman_auth" {
		t.Errorf("generated auth user = %q, want doorman_auth", user)
	}
}

func TestDefaultAuthSecretRefsRejectsConflictingUsers(t *testing.T) {
	spec := specWithPools(map[string]v1alpha1.PoolSpec{
		"app":     {AuthQuery: &v1alpha1.AuthQuerySpec{User: "doorman_auth"}},
		"reports": {AuthQuery: &v1alpha1.AuthQuerySpec{User: "other_auth"}},
	})

	if _, _, err := DefaultAuthSecretRefs(spec); err == nil {
		t.Fatal("pools omitting passwordSecretRef with different users must be rejected")
	}
}

func TestDefaultAuthSecretRefsIgnoresStaticUserPools(t *testing.T) {
	spec := specWithPools(map[string]v1alpha1.PoolSpec{
		"app": {Users: []v1alpha1.UserSpec{{
			Username: "app",
			PasswordSecretRef: machineryapi.SecretKeySelector{
				LocalObjectReference: machineryapi.LocalObjectReference{Name: "app-pass"},
				Key:                  "password",
			},
		}}},
	})

	normalized, user, err := DefaultAuthSecretRefs(spec)
	if err != nil {
		t.Fatalf("DefaultAuthSecretRefs: %v", err)
	}
	if user != "" {
		t.Errorf("nothing generated: auth user must be empty, got %q", user)
	}
	if normalized.Pools["app"].AuthQuery != nil {
		t.Error("static-user pool must stay untouched")
	}
}
