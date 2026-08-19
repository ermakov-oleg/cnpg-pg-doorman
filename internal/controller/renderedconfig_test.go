package controller

import (
	"context"
	"strings"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
)

const (
	ns          = "ns"
	clusterName = "pg"
	crName      = "pg-doorman-cfg"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	for _, add := range []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme, v1alpha1.AddToScheme, cnpgv1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			t.Fatal(err)
		}
	}
	return scheme
}

func cluster() *cnpgv1.Cluster {
	return &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: ns},
		Spec: cnpgv1.ClusterSpec{Plugins: []cnpgv1.PluginConfiguration{{
			Name:       "pg-doorman.cnpg.io",
			Parameters: map[string]string{"configName": crName},
		}}},
	}
}

func pgDoorman(secretName string) *v1alpha1.PgDoorman {
	cr := &v1alpha1.PgDoorman{
		ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: ns},
		Spec: v1alpha1.PgDoormanSpec{
			ClusterRef: machineryapi.LocalObjectReference{Name: clusterName},
			Pools: map[string]v1alpha1.PoolSpec{
				"app": {AuthQuery: &v1alpha1.AuthQuerySpec{User: "doorman_auth"}},
			},
		},
	}
	if secretName != "" {
		cr.Spec.Pools["app"] = v1alpha1.PoolSpec{
			AuthQuery: &v1alpha1.AuthQuerySpec{
				User: "doorman_auth",
				PasswordSecretRef: &machineryapi.SecretKeySelector{
					LocalObjectReference: machineryapi.LocalObjectReference{Name: secretName},
					Key:                  "password",
				},
			},
		}
	}
	return cr
}

func labeledSecret(name, forCluster string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns,
			Labels: map[string]string{ClusterLabel: forCluster},
		},
		Data: map[string][]byte{"password": []byte("s3cr3t")},
	}
}

func newReconciler(t *testing.T, objs ...client.Object) *RenderedConfigReconciler {
	t.Helper()
	t.Setenv("SIDECAR_IMAGE", "wrapper:test")
	cl := fake.NewClientBuilder().WithScheme(newScheme(t)).
		WithStatusSubresource(&v1alpha1.PgDoorman{}).
		WithObjects(objs...).Build()
	return &RenderedConfigReconciler{Client: cl}
}

func reconcile(t *testing.T, r *RenderedConfigReconciler) ctrl.Result {
	t.Helper()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: ns, Name: crName},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	return res
}

func renderedSecret(t *testing.T, r *RenderedConfigReconciler) *corev1.Secret {
	t.Helper()
	var secret corev1.Secret
	if err := r.Get(context.Background(), types.NamespacedName{
		Namespace: ns, Name: RenderedSecretName(clusterName),
	}, &secret); err != nil {
		t.Fatalf("rendered secret: %v", err)
	}
	return &secret
}

func TestReconcileRendersConfigSecret(t *testing.T) {
	r := newReconciler(t, cluster(), pgDoorman("app-pass"), labeledSecret("app-pass", clusterName))

	reconcile(t, r)

	secret := renderedSecret(t, r)
	cfg := string(secret.Data[ConfigKey])
	if !strings.Contains(cfg, "s3cr3t") {
		t.Errorf("rendered config must contain the resolved password, got:\n%s", cfg)
	}
	if !strings.Contains(cfg, "tls_certificate") {
		t.Errorf("rendered config must carry TLS paths, got:\n%s", cfg)
	}
	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].Name != clusterName {
		t.Errorf("rendered secret must be owned by the cluster, got %+v", secret.OwnerReferences)
	}
	if secret.Labels[ClusterLabel] != clusterName {
		t.Errorf("rendered secret must carry the cluster label, got %v", secret.Labels)
	}
}

func TestReconcileGeneratedAdminPasswordIsStable(t *testing.T) {
	r := newReconciler(t, cluster(), pgDoorman(""))

	reconcile(t, r)
	first := string(renderedSecret(t, r).Data["admin-password"])
	if first == "" {
		t.Fatal("generated admin password must be persisted in the secret")
	}

	reconcile(t, r)
	second := string(renderedSecret(t, r).Data["admin-password"])
	if first != second {
		t.Errorf("generated admin password must be stable across re-renders: %q != %q", first, second)
	}
}

func TestReconcileRejectsUnlabeledSecret(t *testing.T) {
	// Confused deputy: a PgDoorman referencing a secret that does not belong
	// to the cluster must not leak it into the rendered config.
	unlabeled := labeledSecret("app-pass", "other-cluster")
	r := newReconciler(t, cluster(), pgDoorman("app-pass"), unlabeled)

	res := reconcile(t, r)
	if res.RequeueAfter == 0 {
		t.Error("rejected render must be retried")
	}

	var secret corev1.Secret
	err := r.Get(context.Background(), types.NamespacedName{
		Namespace: ns, Name: RenderedSecretName(clusterName),
	}, &secret)
	if err == nil {
		t.Error("no secret must be rendered from a foreign referenced secret")
	}
}

func TestReconcileSkipsForeignCluster(t *testing.T) {
	// The cluster references another configName: nothing to render.
	c := cluster()
	c.Spec.Plugins[0].Parameters["configName"] = "another"
	r := newReconciler(t, c, pgDoorman(""))

	reconcile(t, r)

	var secret corev1.Secret
	err := r.Get(context.Background(), types.NamespacedName{
		Namespace: ns, Name: RenderedSecretName(clusterName),
	}, &secret)
	if err == nil {
		t.Error("no secret must be rendered when the cluster uses a different CR")
	}
}

func TestFinalizerLifecycle(t *testing.T) {
	r := newReconciler(t, cluster(), pgDoorman(""))

	// In use: finalizer is added.
	reconcile(t, r)
	var cr v1alpha1.PgDoorman
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: crName}, &cr); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range cr.Finalizers {
		if f == Finalizer {
			found = true
		}
	}
	if !found {
		t.Fatalf("finalizer must be added while the cluster references the CR, got %v", cr.Finalizers)
	}

	// Deletion while in use: blocked (finalizer stays).
	if err := r.Delete(context.Background(), &cr); err != nil {
		t.Fatal(err)
	}
	res := reconcile(t, r)
	if res.RequeueAfter == 0 {
		t.Error("blocked deletion must requeue")
	}
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: crName}, &cr); err != nil {
		t.Fatalf("CR must still exist while referenced: %v", err)
	}

	// Cluster gone: finalizer released, object deleted.
	var c cnpgv1.Cluster
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: clusterName}, &c); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(context.Background(), &c); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r)
	err := r.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: crName}, &cr)
	if err == nil {
		t.Error("CR must be deleted once the finalizer is released")
	}
}

func TestRenderedConditionLifecycle(t *testing.T) {
	unlabeled := labeledSecret("app-pass", "other-cluster")
	r := newReconciler(t, cluster(), pgDoorman("app-pass"), unlabeled)

	// Foreign secret: Rendered=False with a reason.
	reconcile(t, r)
	var cr v1alpha1.PgDoorman
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: crName}, &cr); err != nil {
		t.Fatal(err)
	}
	cond := findCondition(cr.Status.Conditions, "Rendered")
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "SecretNotAllowed" {
		t.Fatalf("Rendered condition = %+v, want False/SecretNotAllowed", cond)
	}

	// Label the secret: Rendered turns True, observedGeneration catches up.
	var secret corev1.Secret
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "app-pass"}, &secret); err != nil {
		t.Fatal(err)
	}
	secret.Labels[ClusterLabel] = clusterName
	if err := r.Update(context.Background(), &secret); err != nil {
		t.Fatal(err)
	}
	reconcile(t, r)
	if err := r.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: crName}, &cr); err != nil {
		t.Fatal(err)
	}
	cond = findCondition(cr.Status.Conditions, "Rendered")
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("Rendered condition = %+v, want True", cond)
	}
	if cr.Status.ObservedGeneration != cr.Generation {
		t.Errorf("observedGeneration = %d, want %d", cr.Status.ObservedGeneration, cr.Generation)
	}
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
