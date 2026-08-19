package operator

import (
	"context"
	"testing"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	machineryapi "github.com/cloudnative-pg/machinery/pkg/api"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/config"
)

func TestValidateConfig_PluginNotEnabled(t *testing.T) {
	cfg := &config.PluginConfiguration{
		Enabled: false,
	}
	result := validateConfig(cfg)
	if len(result.ValidationErrors) != 0 {
		t.Errorf("expected no validation errors for disabled plugin, got %d", len(result.ValidationErrors))
	}
}

func TestValidateConfig_PluginEnabled_Valid(t *testing.T) {
	cfg := &config.PluginConfiguration{
		Enabled:      true,
		PoolerPort:   6432,
		MetricsPort:  9127,
		ConfigName:   "my-config",
		SidecarImage: "ghcr.io/example/pg-doorman:latest",
	}
	result := validateConfig(cfg)
	if len(result.ValidationErrors) != 0 {
		t.Errorf("expected no validation errors for valid config, got %d", len(result.ValidationErrors))
	}
}

func TestValidateConfig_PluginEnabled_Invalid(t *testing.T) {
	cfg := &config.PluginConfiguration{
		Enabled: true,
		// Missing ConfigName and SidecarImage
		PoolerPort:  6432,
		MetricsPort: 9127,
	}
	result := validateConfig(cfg)
	if len(result.ValidationErrors) == 0 {
		t.Error("expected validation errors for invalid config")
	}
}

func TestValidateConfig_ParseErrors(t *testing.T) {
	cfg := &config.PluginConfiguration{
		Enabled:      true,
		PoolerPort:   6432,
		MetricsPort:  9127,
		ConfigName:   "my-config",
		SidecarImage: "ghcr.io/example/pg-doorman:latest",
		ParseErrors:  []string{"poolerPort: invalid integer \"abc\""},
	}
	result := validateConfig(cfg)
	if len(result.ValidationErrors) == 0 {
		t.Error("expected validation errors for config with parse errors")
	}
}

func TestValidateClusterRef(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	cr := &v1alpha1.PgDoorman{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "ns"},
		Spec: v1alpha1.PgDoormanSpec{
			ClusterRef: machineryapi.LocalObjectReference{Name: "owner-cluster"},
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).Build()
	impl := Implementation{Client: cl}

	mk := func(clusterName, configName string) *cnpgv1.Cluster {
		return &cnpgv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName, Namespace: "ns"},
			Spec: cnpgv1.ClusterSpec{Plugins: []cnpgv1.PluginConfiguration{{
				Name:       "pg-doorman.cnpg.io",
				Parameters: map[string]string{"configName": configName},
			}}},
		}
	}

	cfgOwner := config.NewFromCluster(mk("owner-cluster", "cfg"))
	if errs := impl.validateClusterRef(context.Background(), mk("owner-cluster", "cfg"), cfgOwner, true); len(errs) != 0 {
		t.Errorf("matching clusterRef must pass, got %v", errs)
	}

	cfgForeign := config.NewFromCluster(mk("other-cluster", "cfg"))
	if errs := impl.validateClusterRef(context.Background(), mk("other-cluster", "cfg"), cfgForeign, false); len(errs) != 1 {
		t.Errorf("foreign clusterRef must be rejected, got %v", errs)
	}

	cfgMissing := config.NewFromCluster(mk("owner-cluster", "absent"))
	if errs := impl.validateClusterRef(context.Background(), mk("owner-cluster", "absent"), cfgMissing, false); len(errs) != 0 {
		t.Errorf("missing CR on create must pass (GitOps ordering), got %v", errs)
	}
	if errs := impl.validateClusterRef(context.Background(), mk("owner-cluster", "absent"), cfgMissing, true); len(errs) != 1 {
		t.Errorf("missing CR on change must be rejected, got %v", errs)
	}
}
