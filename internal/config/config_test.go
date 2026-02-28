package config

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestNewFromCluster_Defaults(t *testing.T) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: cnpgv1.ClusterSpec{
			Plugins: []cnpgv1.PluginConfiguration{
				{
					Name: "pg-doorman.cnpg.io",
					Parameters: map[string]string{
						"configName": "my-config",
					},
				},
			},
		},
	}

	cfg := NewFromCluster(cluster)
	if cfg.PoolerPort != DefaultPoolerPort {
		t.Errorf("expected default pooler port %d, got %d", DefaultPoolerPort, cfg.PoolerPort)
	}
	if cfg.MetricsPort != DefaultMetricsPort {
		t.Errorf("expected default metrics port %d, got %d", DefaultMetricsPort, cfg.MetricsPort)
	}
	if cfg.ConfigName != "my-config" {
		t.Errorf("expected configName 'my-config', got %q", cfg.ConfigName)
	}
}

func TestNewFromCluster_CustomPorts(t *testing.T) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: cnpgv1.ClusterSpec{
			Plugins: []cnpgv1.PluginConfiguration{
				{
					Name: "pg-doorman.cnpg.io",
					Parameters: map[string]string{
						"configName":  "my-config",
						"poolerPort":  "7432",
						"metricsPort": "9200",
					},
				},
			},
		},
	}

	cfg := NewFromCluster(cluster)
	if cfg.PoolerPort != 7432 {
		t.Errorf("expected pooler port 7432, got %d", cfg.PoolerPort)
	}
	if cfg.MetricsPort != 9200 {
		t.Errorf("expected metrics port 9200, got %d", cfg.MetricsPort)
	}
}

func TestNewFromCluster_DisabledPlugin(t *testing.T) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: cnpgv1.ClusterSpec{
			Plugins: []cnpgv1.PluginConfiguration{
				{
					Name:    "pg-doorman.cnpg.io",
					Enabled: ptr.To(false),
					Parameters: map[string]string{
						"configName": "my-config",
						"poolerPort": "7432",
					},
				},
			},
		},
	}

	cfg := NewFromCluster(cluster)
	// Disabled plugin should not be parsed, so defaults apply
	if cfg.PoolerPort != DefaultPoolerPort {
		t.Errorf("expected default pooler port for disabled plugin, got %d", cfg.PoolerPort)
	}
	if cfg.ConfigName != "" {
		t.Errorf("expected empty configName for disabled plugin, got %q", cfg.ConfigName)
	}
}

func TestNewFromCluster_EnabledNilMeansEnabled(t *testing.T) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: cnpgv1.ClusterSpec{
			Plugins: []cnpgv1.PluginConfiguration{
				{
					Name: "pg-doorman.cnpg.io",
					// Enabled is nil = true by default
					Parameters: map[string]string{
						"configName": "my-config",
					},
				},
			},
		},
	}

	cfg := NewFromCluster(cluster)
	if cfg.ConfigName != "my-config" {
		t.Errorf("expected configName 'my-config' for nil-enabled plugin, got %q", cfg.ConfigName)
	}
}

func TestNewFromCluster_ExplicitlyEnabled(t *testing.T) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: cnpgv1.ClusterSpec{
			Plugins: []cnpgv1.PluginConfiguration{
				{
					Name:    "pg-doorman.cnpg.io",
					Enabled: ptr.To(true),
					Parameters: map[string]string{
						"configName": "my-config",
					},
				},
			},
		},
	}

	cfg := NewFromCluster(cluster)
	if cfg.ConfigName != "my-config" {
		t.Errorf("expected configName for explicitly enabled plugin, got %q", cfg.ConfigName)
	}
}

func TestNewFromCluster_WrongPlugin(t *testing.T) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: cnpgv1.ClusterSpec{
			Plugins: []cnpgv1.PluginConfiguration{
				{
					Name: "other-plugin.cnpg.io",
					Parameters: map[string]string{
						"configName": "other-config",
					},
				},
			},
		},
	}

	cfg := NewFromCluster(cluster)
	if cfg.ConfigName != "" {
		t.Errorf("expected empty configName for wrong plugin, got %q", cfg.ConfigName)
	}
}

func TestNewFromCluster_InvalidPort(t *testing.T) {
	cluster := &cnpgv1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: cnpgv1.ClusterSpec{
			Plugins: []cnpgv1.PluginConfiguration{
				{
					Name: "pg-doorman.cnpg.io",
					Parameters: map[string]string{
						"configName": "my-config",
						"poolerPort": "not-a-number",
					},
				},
			},
		},
	}

	cfg := NewFromCluster(cluster)
	// Invalid port string should keep default
	if cfg.PoolerPort != DefaultPoolerPort {
		t.Errorf("expected default pooler port for invalid port string, got %d", cfg.PoolerPort)
	}
}

func TestValidate_Valid(t *testing.T) {
	cfg := &PluginConfiguration{
		PoolerPort:  6432,
		MetricsPort: 9127,
		ConfigName:  "my-config",
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected valid config, got error: %v", err)
	}
}

func TestValidate_MissingConfigName(t *testing.T) {
	cfg := &PluginConfiguration{
		PoolerPort:  6432,
		MetricsPort: 9127,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for missing configName")
	}
}

func TestValidate_InvalidPoolerPort(t *testing.T) {
	tests := []struct {
		name string
		port int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too high", 70000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &PluginConfiguration{
				PoolerPort:  tt.port,
				MetricsPort: 9127,
				ConfigName:  "my-config",
			}
			if err := cfg.Validate(); err == nil {
				t.Error("expected error for invalid pooler port")
			}
		})
	}
}

func TestValidate_SamePorts(t *testing.T) {
	cfg := &PluginConfiguration{
		PoolerPort:  6432,
		MetricsPort: 6432,
		ConfigName:  "my-config",
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for same pooler and metrics port")
	}
}

func TestSetDefaults(t *testing.T) {
	params := SetDefaults(nil)
	if params["poolerPort"] != "6432" {
		t.Errorf("expected default poolerPort, got %q", params["poolerPort"])
	}
	if params["metricsPort"] != "9127" {
		t.Errorf("expected default metricsPort, got %q", params["metricsPort"])
	}
}

func TestSetDefaults_PreservesExisting(t *testing.T) {
	params := map[string]string{
		"poolerPort": "7432",
		"configName": "my-config",
	}
	result := SetDefaults(params)
	if result["poolerPort"] != "7432" {
		t.Errorf("expected preserved poolerPort 7432, got %q", result["poolerPort"])
	}
	if result["metricsPort"] != "9127" {
		t.Errorf("expected default metricsPort, got %q", result["metricsPort"])
	}
	if result["configName"] != "my-config" {
		t.Errorf("expected preserved configName, got %q", result["configName"])
	}
}
