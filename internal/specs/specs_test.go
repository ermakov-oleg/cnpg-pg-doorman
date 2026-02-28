package specs

import (
	"testing"

	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
)

func TestCollectSecretNames_WithAdminPasswordSecret(t *testing.T) {
	spec := &v1alpha1.PgDoormanSpec{
		General: &v1alpha1.GeneralSpec{
			AdminPasswordSecretRef: &machineryapi.SecretKeySelector{
				LocalObjectReference: machineryapi.LocalObjectReference{Name: "admin-secret"},
				Key:                  "password",
			},
		},
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
	}

	names := CollectSecretNames(spec)
	if len(names) != 2 {
		t.Fatalf("expected 2 secrets, got %d: %v", len(names), names)
	}

	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["admin-secret"] {
		t.Error("expected admin-secret in collected names")
	}
	if !found["auth-secret"] {
		t.Error("expected auth-secret in collected names")
	}
}

func TestCollectSecretNames_EmptyPools(t *testing.T) {
	spec := &v1alpha1.PgDoormanSpec{
		Pools: map[string]v1alpha1.PoolSpec{},
	}

	names := CollectSecretNames(spec)
	if len(names) != 0 {
		t.Fatalf("expected 0 secrets, got %d: %v", len(names), names)
	}
}

func TestCollectSecretNames_DuplicateSecrets(t *testing.T) {
	spec := &v1alpha1.PgDoormanSpec{
		Pools: map[string]v1alpha1.PoolSpec{
			"app": {
				Users: []v1alpha1.UserSpec{
					{Username: "u1", PasswordSecretRef: machineryapi.SecretKeySelector{
						LocalObjectReference: machineryapi.LocalObjectReference{Name: "same-secret"},
						Key:                  "password",
					}},
					{Username: "u2", PasswordSecretRef: machineryapi.SecretKeySelector{
						LocalObjectReference: machineryapi.LocalObjectReference{Name: "same-secret"},
						Key:                  "password",
					}},
				},
			},
		},
	}

	names := CollectSecretNames(spec)
	if len(names) != 1 {
		t.Fatalf("expected 1 unique secret, got %d: %v", len(names), names)
	}
}
