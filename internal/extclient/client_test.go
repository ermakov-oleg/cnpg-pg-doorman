package extclient

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
)

// countingClient counts Get calls that reach the underlying client.
type countingClient struct {
	client.Client
	getCalls int
}

func (c *countingClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	obj client.Object,
	opts ...client.GetOption,
) error {
	c.getCalls++
	return c.Client.Get(ctx, key, obj, opts...)
}

func newTestClient(t *testing.T, objs ...client.Object) (*ExtendedClient, *countingClient) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
	counting := &countingClient{Client: base}
	ext, ok := NewExtendedClient(counting).(*ExtendedClient)
	if !ok {
		t.Fatal("NewExtendedClient did not return *ExtendedClient")
	}
	return ext, counting
}

func testSecret(name, ns, value string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Data:       map[string][]byte{"password": []byte(value)},
	}
}

func getSecret(t *testing.T, ext *ExtendedClient, name, ns string) *corev1.Secret {
	t.Helper()
	var secret corev1.Secret
	if err := ext.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: name}, &secret); err != nil {
		t.Fatalf("Get secret %s/%s: %v", ns, name, err)
	}
	return &secret
}

func TestGetCachesSecretWithinTTL(t *testing.T) {
	ext, counting := newTestClient(t, testSecret("s1", "ns", "pw"))

	first := getSecret(t, ext, "s1", "ns")
	second := getSecret(t, ext, "s1", "ns")

	if counting.getCalls != 1 {
		t.Errorf("underlying Get called %d times, want 1 (second Get must be served from cache)", counting.getCalls)
	}
	if string(first.Data["password"]) != "pw" || string(second.Data["password"]) != "pw" {
		t.Errorf("cached secret data mismatch: first=%q second=%q", first.Data["password"], second.Data["password"])
	}
}

func TestGetRefetchesAfterTTLExpiry(t *testing.T) {
	ext, counting := newTestClient(t, testSecret("s1", "ns", "old"))

	cached := getSecret(t, ext, "s1", "ns")

	// Change the secret behind the cache's back (bypassing Update invalidation).
	cached.Data["password"] = []byte("new")
	if err := counting.Update(context.Background(), cached); err != nil {
		t.Fatal(err)
	}

	// Backdate the cache entry so the TTL is expired.
	ext.mux.Lock()
	for i := range ext.cachedObjects {
		ext.cachedObjects[i].fetchUnixTime = time.Now().Add(-time.Hour).Unix()
	}
	ext.mux.Unlock()

	refetched := getSecret(t, ext, "s1", "ns")
	if counting.getCalls != 2 {
		t.Errorf("underlying Get called %d times, want 2 (expired entry must be refetched)", counting.getCalls)
	}
	if string(refetched.Data["password"]) != "new" {
		t.Errorf("expired entry served stale data: %q", refetched.Data["password"])
	}
}

func TestGetSeparateKeysDoNotCollide(t *testing.T) {
	ext, counting := newTestClient(t,
		testSecret("s1", "ns", "pw1"),
		testSecret("s2", "ns", "pw2"),
		testSecret("s1", "other", "pw3"),
	)

	if got := string(getSecret(t, ext, "s1", "ns").Data["password"]); got != "pw1" {
		t.Errorf("ns/s1 = %q, want pw1", got)
	}
	if got := string(getSecret(t, ext, "s2", "ns").Data["password"]); got != "pw2" {
		t.Errorf("ns/s2 = %q, want pw2", got)
	}
	if got := string(getSecret(t, ext, "s1", "other").Data["password"]); got != "pw3" {
		t.Errorf("other/s1 = %q, want pw3", got)
	}
	if counting.getCalls != 3 {
		t.Fatalf("underlying Get called %d times, want 3", counting.getCalls)
	}

	// All three must now be cached under distinct keys.
	getSecret(t, ext, "s1", "ns")
	getSecret(t, ext, "s2", "ns")
	getSecret(t, ext, "s1", "other")
	if counting.getCalls != 3 {
		t.Errorf("underlying Get called %d times after cached reads, want 3", counting.getCalls)
	}
}

func TestGetDifferentTypesWithSameNameDoNotCollide(t *testing.T) {
	doorman := &v1alpha1.PgDoorman{ObjectMeta: metav1.ObjectMeta{Name: "same", Namespace: "ns"}}
	ext, counting := newTestClient(t, testSecret("same", "ns", "pw"), doorman)

	getSecret(t, ext, "same", "ns")

	var cr v1alpha1.PgDoorman
	if err := ext.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "same"}, &cr); err != nil {
		t.Fatalf("Get PgDoorman: %v", err)
	}
	if counting.getCalls != 2 {
		t.Errorf("underlying Get called %d times, want 2 (Secret cache entry must not satisfy PgDoorman Get)",
			counting.getCalls)
	}
}

func TestGetUncachedTypeAlwaysHitsUnderlyingClient(t *testing.T) {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cm", Namespace: "ns"}}
	ext, counting := newTestClient(t, cm)

	for i := 0; i < 2; i++ {
		var out corev1.ConfigMap
		if err := ext.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "cm"}, &out); err != nil {
			t.Fatalf("Get configmap: %v", err)
		}
	}
	if counting.getCalls != 2 {
		t.Errorf("underlying Get called %d times, want 2 (ConfigMap must not be cached)", counting.getCalls)
	}
}

func TestUpdateInvalidatesCacheEntry(t *testing.T) {
	ext, counting := newTestClient(t, testSecret("s1", "ns", "old"))

	secret := getSecret(t, ext, "s1", "ns")

	secret.Data["password"] = []byte("new")
	if err := ext.Update(context.Background(), secret); err != nil {
		t.Fatal(err)
	}

	updated := getSecret(t, ext, "s1", "ns")
	if counting.getCalls != 2 {
		t.Errorf("underlying Get called %d times, want 2 (Update must invalidate the cache entry)", counting.getCalls)
	}
	if string(updated.Data["password"]) != "new" {
		t.Errorf("Get after Update returned stale data: %q", updated.Data["password"])
	}
}

func TestDeleteInvalidatesCacheEntry(t *testing.T) {
	ext, counting := newTestClient(t, testSecret("s1", "ns", "pw"))

	secret := getSecret(t, ext, "s1", "ns")

	if err := ext.Delete(context.Background(), secret); err != nil {
		t.Fatal(err)
	}

	var out corev1.Secret
	err := ext.Get(context.Background(), client.ObjectKey{Namespace: "ns", Name: "s1"}, &out)
	if err == nil {
		t.Error("Get after Delete must not be served from cache")
	}
	if counting.getCalls != 2 {
		t.Errorf("underlying Get called %d times, want 2", counting.getCalls)
	}
}
