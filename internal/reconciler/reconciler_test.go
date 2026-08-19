package reconciler

import (
	"context"
	"encoding/json"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i/pkg/reconciler"
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/specs"
	"github.com/ermakov-oleg/cnpg-pg-doorman/pkg/metadata"
)

const (
	testNamespace  = "ns"
	testCluster    = "pg-test"
	testConfigName = "doorman-cfg"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := cnpgv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func newImplementation(t *testing.T, objs ...client.Object) Implementation {
	t.Helper()
	cl := fake.NewClientBuilder().WithScheme(newTestScheme(t)).WithObjects(objs...).Build()
	return Implementation{Client: cl}
}

func clusterWithPlugin() *cnpgv1.Cluster {
	return &cnpgv1.Cluster{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Cluster",
			APIVersion: "postgresql.cnpg.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{Name: testCluster, Namespace: testNamespace},
		Spec: cnpgv1.ClusterSpec{
			Plugins: []cnpgv1.PluginConfiguration{
				{
					Name:       metadata.PluginName,
					Parameters: map[string]string{"configName": testConfigName},
				},
			},
		},
	}
}

func clusterWithoutPlugin() *cnpgv1.Cluster {
	cluster := clusterWithPlugin()
	cluster.Spec.Plugins = nil
	return cluster
}

func clusterRequest(t *testing.T, cluster *cnpgv1.Cluster) *reconciler.ReconcilerHooksRequest {
	t.Helper()
	definition, err := json.Marshal(cluster)
	if err != nil {
		t.Fatal(err)
	}
	return &reconciler.ReconcilerHooksRequest{ResourceDefinition: definition}
}

func pgDoormanWithSecret(secretName string) *v1alpha1.PgDoorman {
	return &v1alpha1.PgDoorman{
		ObjectMeta: metav1.ObjectMeta{Name: testConfigName, Namespace: testNamespace},
		Spec: v1alpha1.PgDoormanSpec{
			ClusterRef: machineryapi.LocalObjectReference{Name: testCluster},
			Pools: map[string]v1alpha1.PoolSpec{
				"db": {
					Users: []v1alpha1.UserSpec{
						{
							Username: "app",
							PasswordSecretRef: machineryapi.SecretKeySelector{
								LocalObjectReference: machineryapi.LocalObjectReference{Name: secretName},
								Key:                  "password",
							},
						},
					},
				},
			},
		},
	}
}

func TestPreRequeuesWhenPgDoormanMissing(t *testing.T) {
	t.Setenv("SIDECAR_IMAGE", "wrapper:test")
	r := newImplementation(t)

	result, err := r.Pre(context.Background(), clusterRequest(t, clusterWithPlugin()))
	if err != nil {
		t.Fatalf("Pre returned error: %v", err)
	}
	if result.Behavior != reconciler.ReconcilerHooksResult_BEHAVIOR_REQUEUE {
		t.Errorf("behavior = %v, want BEHAVIOR_REQUEUE", result.Behavior)
	}
}

func TestPreContinuesForNonClusterKind(t *testing.T) {
	t.Setenv("SIDECAR_IMAGE", "wrapper:test")
	r := newImplementation(t)

	request := &reconciler.ReconcilerHooksRequest{
		ResourceDefinition: []byte(`{"kind":"Backup","apiVersion":"postgresql.cnpg.io/v1"}`),
	}
	result, err := r.Pre(context.Background(), request)
	if err != nil {
		t.Fatalf("Pre returned error: %v", err)
	}
	if result.Behavior != reconciler.ReconcilerHooksResult_BEHAVIOR_CONTINUE {
		t.Errorf("behavior = %v, want BEHAVIOR_CONTINUE", result.Behavior)
	}
}

func TestPreEnsuresServiceAndRemovesLegacyRBAC(t *testing.T) {
	t.Setenv("SIDECAR_IMAGE", "wrapper:test")
	r := newImplementation(t, pgDoormanWithSecret("app-secret"))
	ctx := context.Background()
	request := clusterRequest(t, clusterWithPlugin())

	// Pre-create legacy RBAC as if left over from the pre-rendered-config era.
	legacyRole := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
		Namespace: testNamespace, Name: specs.GetRBACName(testCluster),
	}}
	legacyBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
		Namespace: testNamespace, Name: specs.GetRBACName(testCluster),
	}}
	if err := r.Client.Create(ctx, legacyRole); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Create(ctx, legacyBinding); err != nil {
		t.Fatal(err)
	}

	result, err := r.Pre(ctx, request)
	if err != nil {
		t.Fatalf("Pre returned error: %v", err)
	}
	if result.Behavior != reconciler.ReconcilerHooksResult_BEHAVIOR_CONTINUE {
		t.Fatalf("behavior = %v, want BEHAVIOR_CONTINUE", result.Behavior)
	}

	rbacKey := client.ObjectKey{Namespace: testNamespace, Name: specs.GetRBACName(testCluster)}
	svcKey := client.ObjectKey{Namespace: testNamespace, Name: specs.GetServiceName(testCluster)}

	// Pods get zero RBAC now: legacy Role/RoleBinding must be gone.
	var role rbacv1.Role
	if err := r.Client.Get(ctx, rbacKey, &role); !apierrs.IsNotFound(err) {
		t.Errorf("legacy role must be deleted, got err=%v", err)
	}
	var roleBinding rbacv1.RoleBinding
	if err := r.Client.Get(ctx, rbacKey, &roleBinding); !apierrs.IsNotFound(err) {
		t.Errorf("legacy rolebinding must be deleted, got err=%v", err)
	}

	var svc corev1.Service
	if err := r.Client.Get(ctx, svcKey, &svc); err != nil {
		t.Fatalf("service not created: %v", err)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].TargetPort.IntValue() != 6432 {
		t.Errorf("service ports = %+v, want single port targeting 6432", svc.Spec.Ports)
	}

	// Second Pre must succeed without touching the Service.
	if _, err := r.Pre(ctx, request); err != nil {
		t.Fatalf("second Pre returned error: %v", err)
	}
	var svc2 corev1.Service
	if err := r.Client.Get(ctx, svcKey, &svc2); err != nil {
		t.Fatal(err)
	}
	if svc2.ResourceVersion != svc.ResourceVersion {
		t.Errorf("service resourceVersion changed on idempotent Pre: %s -> %s", svc.ResourceVersion, svc2.ResourceVersion)
	}
}

func TestPreCleansUpResourcesWhenPluginDisabled(t *testing.T) {
	t.Setenv("SIDECAR_IMAGE", "wrapper:test")
	r := newImplementation(t, pgDoormanWithSecret("app-secret"))
	ctx := context.Background()

	if _, err := r.Pre(ctx, clusterRequest(t, clusterWithPlugin())); err != nil {
		t.Fatalf("Pre with enabled plugin: %v", err)
	}

	result, err := r.Pre(ctx, clusterRequest(t, clusterWithoutPlugin()))
	if err != nil {
		t.Fatalf("Pre with disabled plugin: %v", err)
	}
	if result.Behavior != reconciler.ReconcilerHooksResult_BEHAVIOR_CONTINUE {
		t.Errorf("behavior = %v, want BEHAVIOR_CONTINUE", result.Behavior)
	}

	rbacKey := client.ObjectKey{Namespace: testNamespace, Name: specs.GetRBACName(testCluster)}
	svcKey := client.ObjectKey{Namespace: testNamespace, Name: specs.GetServiceName(testCluster)}

	if err := r.Client.Get(ctx, rbacKey, &rbacv1.Role{}); !apierrs.IsNotFound(err) {
		t.Errorf("role still present after cleanup, err = %v", err)
	}
	if err := r.Client.Get(ctx, rbacKey, &rbacv1.RoleBinding{}); !apierrs.IsNotFound(err) {
		t.Errorf("rolebinding still present after cleanup, err = %v", err)
	}
	if err := r.Client.Get(ctx, svcKey, &corev1.Service{}); !apierrs.IsNotFound(err) {
		t.Errorf("service still present after cleanup, err = %v", err)
	}
}

func TestPreContinuesWhenCRMissingOnRunningCluster(t *testing.T) {
	// A missing CR must never freeze a RUNNING cluster: REQUEUE stops the
	// whole reconcile (no failover, no pod replacement).
	t.Setenv("SIDECAR_IMAGE", "wrapper:test")
	r := newImplementation(t)
	c := clusterWithPlugin()
	c.Status.CurrentPrimary = testCluster + "-1"

	result, err := r.Pre(context.Background(), clusterRequest(t, c))
	if err != nil {
		t.Fatalf("Pre returned error: %v", err)
	}
	if result.Behavior != reconciler.ReconcilerHooksResult_BEHAVIOR_CONTINUE {
		t.Errorf("behavior = %v, want CONTINUE for a running cluster", result.Behavior)
	}
}

func TestPreRequeueAfterSetOnCreation(t *testing.T) {
	t.Setenv("SIDECAR_IMAGE", "wrapper:test")
	r := newImplementation(t)

	result, err := r.Pre(context.Background(), clusterRequest(t, clusterWithPlugin()))
	if err != nil {
		t.Fatalf("Pre returned error: %v", err)
	}
	if result.Behavior != reconciler.ReconcilerHooksResult_BEHAVIOR_REQUEUE {
		t.Fatalf("behavior = %v, want REQUEUE at creation", result.Behavior)
	}
	if result.RequeueAfter == 0 {
		t.Error("RequeueAfter must be set to avoid exponential workqueue backoff")
	}
}
