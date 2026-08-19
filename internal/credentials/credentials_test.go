package credentials

import (
	"context"
	"testing"

	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/configgen"
)

const testNamespace = "ns"

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func secretWith(name, key, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Data:       map[string][]byte{key: []byte(value)},
	}
}

func secretRef(name, key string) *machineryapi.SecretKeySelector {
	return &machineryapi.SecretKeySelector{
		LocalObjectReference: machineryapi.LocalObjectReference{Name: name},
		Key:                  key,
	}
}

func fullSpec() *v1alpha1.PgDoormanSpec {
	return &v1alpha1.PgDoormanSpec{
		Pools: map[string]v1alpha1.PoolSpec{
			"db": {
				AuthQuery: &v1alpha1.AuthQuerySpec{
					User:              "auth_user",
					PasswordSecretRef: secretRef("auth-secret", "password"),
				},
				Users: []v1alpha1.UserSpec{
					{Username: "app", PasswordSecretRef: *secretRef("app-secret", "password")},
				},
			},
		},
		General: &v1alpha1.GeneralSpec{
			AdminPasswordSecretRef: secretRef("admin-secret", "admin"),
		},
	}
}

func TestResolvePasswordsResolvesAllRefs(t *testing.T) {
	cl := newFakeClient(t,
		secretWith("auth-secret", "password", "auth-pw"),
		secretWith("app-secret", "password", "app-pw"),
		secretWith("admin-secret", "admin", "admin-pw"),
	)

	passwords, err := ResolvePasswords(context.Background(), cl, testNamespace, fullSpec())
	if err != nil {
		t.Fatalf("ResolvePasswords: %v", err)
	}

	if len(passwords) != 3 {
		t.Errorf("got %d passwords, want 3: %v", len(passwords), passwords)
	}
	if got := passwords[configgen.PasswordKey("db", "auth_query", "auth_user")]; got != "auth-pw" {
		t.Errorf("auth_query password = %q, want auth-pw", got)
	}
	if got := passwords[configgen.PasswordKey("db", "user", "app")]; got != "app-pw" {
		t.Errorf("user password = %q, want app-pw", got)
	}
	if got := passwords[configgen.AdminPasswordKey]; got != "admin-pw" {
		t.Errorf("admin password = %q, want admin-pw", got)
	}
}

func TestResolvePasswordsErrorsOnMissingSecret(t *testing.T) {
	cl := newFakeClient(t) // no secrets at all

	spec := &v1alpha1.PgDoormanSpec{
		Pools: map[string]v1alpha1.PoolSpec{
			"db": {
				Users: []v1alpha1.UserSpec{
					{Username: "app", PasswordSecretRef: *secretRef("missing-secret", "password")},
				},
			},
		},
	}

	if _, err := ResolvePasswords(context.Background(), cl, testNamespace, spec); err == nil {
		t.Error("expected error for missing secret, got nil")
	}
}

func TestResolvePasswordsErrorsOnMissingKey(t *testing.T) {
	cl := newFakeClient(t, secretWith("app-secret", "other-key", "pw"))

	spec := &v1alpha1.PgDoormanSpec{
		Pools: map[string]v1alpha1.PoolSpec{
			"db": {
				Users: []v1alpha1.UserSpec{
					{Username: "app", PasswordSecretRef: *secretRef("app-secret", "password")},
				},
			},
		},
	}

	if _, err := ResolvePasswords(context.Background(), cl, testNamespace, spec); err == nil {
		t.Error("expected error for missing key in secret, got nil")
	}
}

func TestCollectSecretVersionsStableAndChangesOnRotation(t *testing.T) {
	cl := newFakeClient(t,
		secretWith("auth-secret", "password", "auth-pw"),
		secretWith("app-secret", "password", "app-pw"),
		secretWith("admin-secret", "admin", "admin-pw"),
	)
	ctx := context.Background()
	spec := fullSpec()

	first, err := CollectSecretVersions(ctx, cl, spec, testNamespace)
	if err != nil {
		t.Fatalf("CollectSecretVersions: %v", err)
	}
	if first == "" {
		t.Fatal("hash must be non-empty when the spec references secrets")
	}

	second, err := CollectSecretVersions(ctx, cl, spec, testNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Errorf("hash not stable without changes: %q != %q", second, first)
	}

	// Rotate one password: the fake client bumps resourceVersion on Update.
	var secret corev1.Secret
	if err := cl.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: "app-secret"}, &secret); err != nil {
		t.Fatal(err)
	}
	secret.Data["password"] = []byte("rotated")
	if err := cl.Update(ctx, &secret); err != nil {
		t.Fatal(err)
	}

	third, err := CollectSecretVersions(ctx, cl, spec, testNamespace)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Error("hash must change after a referenced secret is updated")
	}
}

func TestCollectSecretVersionsEmptyWithoutRefs(t *testing.T) {
	cl := newFakeClient(t)

	spec := &v1alpha1.PgDoormanSpec{
		Pools: map[string]v1alpha1.PoolSpec{
			"db": {
				AuthQuery: &v1alpha1.AuthQuerySpec{User: "auth_user"}, // no secret ref
			},
		},
	}

	hash, err := CollectSecretVersions(context.Background(), cl, spec, testNamespace)
	if err != nil {
		t.Fatalf("CollectSecretVersions: %v", err)
	}
	if hash != "" {
		t.Errorf("hash = %q, want empty string when no secrets are referenced", hash)
	}
}

func TestCollectSecretVersionsErrorsOnMissingSecret(t *testing.T) {
	cl := newFakeClient(t)

	if _, err := CollectSecretVersions(context.Background(), cl, fullSpec(), testNamespace); err == nil {
		t.Error("expected error for missing referenced secret, got nil")
	}
}
