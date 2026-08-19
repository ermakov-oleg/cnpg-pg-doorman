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

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/specs"
	"github.com/o-ermakov/cnpg-pg-doorman/pkg/metadata"
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

func secretResourceNames(t *testing.T, role *rbacv1.Role) []string {
	t.Helper()
	for _, rule := range role.Rules {
		for _, res := range rule.Resources {
			if res == "secrets" {
				return rule.ResourceNames
			}
		}
	}
	t.Fatalf("no secrets rule found in role rules: %+v", role.Rules)
	return nil
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

func TestPreCreatesRBACAndServiceIdempotently(t *testing.T) {
	t.Setenv("SIDECAR_IMAGE", "wrapper:test")
	r := newImplementation(t, pgDoormanWithSecret("app-secret"))
	ctx := context.Background()
	request := clusterRequest(t, clusterWithPlugin())

	result, err := r.Pre(ctx, request)
	if err != nil {
		t.Fatalf("first Pre returned error: %v", err)
	}
	if result.Behavior != reconciler.ReconcilerHooksResult_BEHAVIOR_CONTINUE {
		t.Fatalf("behavior = %v, want BEHAVIOR_CONTINUE", result.Behavior)
	}

	rbacKey := client.ObjectKey{Namespace: testNamespace, Name: specs.GetRBACName(testCluster)}
	svcKey := client.ObjectKey{Namespace: testNamespace, Name: specs.GetServiceName(testCluster)}

	var role rbacv1.Role
	if err := r.Client.Get(ctx, rbacKey, &role); err != nil {
		t.Fatalf("role not created: %v", err)
	}
	if names := secretResourceNames(t, &role); len(names) != 1 || names[0] != "app-secret" {
		t.Errorf("role secrets resourceNames = %v, want [app-secret]", names)
	}

	var roleBinding rbacv1.RoleBinding
	if err := r.Client.Get(ctx, rbacKey, &roleBinding); err != nil {
		t.Fatalf("rolebinding not created: %v", err)
	}
	if roleBinding.RoleRef.Name != specs.GetRBACName(testCluster) {
		t.Errorf("rolebinding roleRef = %q, want %q", roleBinding.RoleRef.Name, specs.GetRBACName(testCluster))
	}

	var svc corev1.Service
	if err := r.Client.Get(ctx, svcKey, &svc); err != nil {
		t.Fatalf("service not created: %v", err)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].TargetPort.IntValue() != 6432 {
		t.Errorf("service ports = %+v, want single port targeting 6432", svc.Spec.Ports)
	}

	// Second Pre must succeed without touching the objects.
	if _, err := r.Pre(ctx, request); err != nil {
		t.Fatalf("second Pre returned error: %v", err)
	}

	var role2 rbacv1.Role
	var roleBinding2 rbacv1.RoleBinding
	var svc2 corev1.Service
	if err := r.Client.Get(ctx, rbacKey, &role2); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, rbacKey, &roleBinding2); err != nil {
		t.Fatal(err)
	}
	if err := r.Client.Get(ctx, svcKey, &svc2); err != nil {
		t.Fatal(err)
	}
	if role2.ResourceVersion != role.ResourceVersion {
		t.Errorf("role resourceVersion changed on idempotent Pre: %s -> %s", role.ResourceVersion, role2.ResourceVersion)
	}
	if roleBinding2.ResourceVersion != roleBinding.ResourceVersion {
		t.Errorf("rolebinding resourceVersion changed on idempotent Pre: %s -> %s",
			roleBinding.ResourceVersion, roleBinding2.ResourceVersion)
	}
	if svc2.ResourceVersion != svc.ResourceVersion {
		t.Errorf("service resourceVersion changed on idempotent Pre: %s -> %s", svc.ResourceVersion, svc2.ResourceVersion)
	}
}

func TestEnsureRoleUpdatesRulesWhenSecretRefsChange(t *testing.T) {
	r := newImplementation(t)
	ctx := context.Background()
	cluster := clusterWithPlugin()

	if err := r.ensureRole(ctx, cluster, pgDoormanWithSecret("secret-a")); err != nil {
		t.Fatalf("first ensureRole: %v", err)
	}
	if err := r.ensureRole(ctx, cluster, pgDoormanWithSecret("secret-b")); err != nil {
		t.Fatalf("second ensureRole: %v", err)
	}

	var role rbacv1.Role
	key := client.ObjectKey{Namespace: testNamespace, Name: specs.GetRBACName(testCluster)}
	if err := r.Client.Get(ctx, key, &role); err != nil {
		t.Fatal(err)
	}
	if names := secretResourceNames(t, &role); len(names) != 1 || names[0] != "secret-b" {
		t.Errorf("role secrets resourceNames = %v, want [secret-b]", names)
	}
}

func TestEnsureRoleBindingRecreatedWhenRoleRefDiffers(t *testing.T) {
	staleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNamespace,
			Name:      specs.GetRBACName(testCluster),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     "legacy-role",
		},
	}
	r := newImplementation(t, staleBinding)
	ctx := context.Background()

	if err := r.ensureRoleBinding(ctx, clusterWithPlugin()); err != nil {
		t.Fatalf("ensureRoleBinding: %v", err)
	}

	var roleBinding rbacv1.RoleBinding
	key := client.ObjectKey{Namespace: testNamespace, Name: specs.GetRBACName(testCluster)}
	if err := r.Client.Get(ctx, key, &roleBinding); err != nil {
		t.Fatal(err)
	}
	if roleBinding.RoleRef.Name != specs.GetRBACName(testCluster) {
		t.Errorf("roleRef = %q, want %q (RoleBinding must be recreated)",
			roleBinding.RoleRef.Name, specs.GetRBACName(testCluster))
	}
	if len(roleBinding.Subjects) != 1 || roleBinding.Subjects[0].Name != testCluster {
		t.Errorf("subjects = %+v, want the cluster ServiceAccount %q", roleBinding.Subjects, testCluster)
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
