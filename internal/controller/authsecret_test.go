package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/specs"
)

func generatedAuthSecret(t *testing.T, r *RenderedConfigReconciler) *corev1.Secret {
	t.Helper()
	var secret corev1.Secret
	if err := r.Get(context.Background(), types.NamespacedName{
		Namespace: ns, Name: specs.GeneratedAuthSecretName(clusterName),
	}, &secret); err != nil {
		t.Fatalf("generated auth secret: %v", err)
	}
	return &secret
}

func TestReconcileGeneratesAuthSecret(t *testing.T) {
	r := newReconciler(t, cluster(), pgDoorman(""))

	reconcile(t, r)

	secret := generatedAuthSecret(t, r)
	if secret.Type != corev1.SecretTypeBasicAuth {
		t.Errorf("secret type = %q, want %q", secret.Type, corev1.SecretTypeBasicAuth)
	}
	if got := string(secret.Data["username"]); got != "doorman_auth" {
		t.Errorf("username = %q, want doorman_auth", got)
	}
	password := string(secret.Data["password"])
	if password == "" {
		t.Fatal("generated secret must carry a non-empty password")
	}
	if secret.Labels[ClusterLabel] != clusterName {
		t.Errorf("generated secret must carry the cluster label, got %v", secret.Labels)
	}
	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Name != clusterName {
		t.Errorf("generated secret must be owned by the cluster, got %+v", secret.OwnerReferences)
	}

	cfg := string(renderedSecret(t, r).Data[ConfigKey])
	if !strings.Contains(cfg, password) {
		t.Errorf("rendered config must contain the generated password, got:\n%s", cfg)
	}
}

func TestReconcileGeneratedAuthSecretIsStable(t *testing.T) {
	r := newReconciler(t, cluster(), pgDoorman(""))

	reconcile(t, r)
	first := string(generatedAuthSecret(t, r).Data["password"])

	reconcile(t, r)
	second := string(generatedAuthSecret(t, r).Data["password"])
	if first != second {
		t.Errorf("generated password must be stable across reconciles: %q != %q", first, second)
	}
}

func TestReconcileKeepsUserProvidedAuthSecret(t *testing.T) {
	// A pre-existing labeled secret under the generated name belongs to the
	// user (BYO password): reuse it, never overwrite.
	own := labeledSecret(specs.GeneratedAuthSecretName(clusterName), clusterName)
	r := newReconciler(t, cluster(), pgDoorman(""), own)

	reconcile(t, r)

	if got := string(generatedAuthSecret(t, r).Data["password"]); got != "s3cr3t" {
		t.Errorf("user-provided secret must not be overwritten, got password %q", got)
	}
	cfg := string(renderedSecret(t, r).Data[ConfigKey])
	if !strings.Contains(cfg, "s3cr3t") {
		t.Errorf("rendered config must contain the user-provided password, got:\n%s", cfg)
	}
}

func TestReconcileRejectsUnlabeledSecretUnderGeneratedName(t *testing.T) {
	// An unlabeled secret squatting the generated name must not be
	// overwritten or leaked: the render is refused like any foreign secret.
	foreign := labeledSecret(specs.GeneratedAuthSecretName(clusterName), "other-cluster")
	r := newReconciler(t, cluster(), pgDoorman(""), foreign)

	res := reconcile(t, r)
	if res.RequeueAfter == 0 {
		t.Error("rejected render must be retried")
	}

	if got := string(generatedAuthSecret(t, r).Data["password"]); got != "s3cr3t" {
		t.Errorf("foreign secret must not be overwritten, got password %q", got)
	}
	var secret corev1.Secret
	err := r.Get(context.Background(), types.NamespacedName{
		Namespace: ns, Name: RenderedSecretName(clusterName),
	}, &secret)
	if err == nil {
		t.Error("no config must be rendered from a foreign secret")
	}
}
