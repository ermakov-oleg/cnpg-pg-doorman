package operator

import (
	"testing"

	"github.com/o-ermakov/cnpg-pg-doorman/internal/config"
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
