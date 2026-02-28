package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"

	"github.com/o-ermakov/cnpg-pg-doorman/pkg/metadata"
)

const (
	DefaultPoolerPort  = 6432
	DefaultMetricsPort = 9127

	ParamPoolerPort  = "poolerPort"
	ParamMetricsPort = "metricsPort"
	ParamConfigName  = "configName"
)

type PluginConfiguration struct {
	Enabled      bool
	PoolerPort   int
	MetricsPort  int
	ConfigName   string
	SidecarImage string
	ParseErrors  []string
}

func NewFromCluster(cluster *cnpgv1.Cluster) *PluginConfiguration {
	cfg := &PluginConfiguration{
		PoolerPort:   DefaultPoolerPort,
		MetricsPort:  DefaultMetricsPort,
		SidecarImage: os.Getenv("SIDECAR_IMAGE"),
	}

	for _, plugin := range cluster.Spec.Plugins {
		if plugin.Name != metadata.PluginName {
			continue
		}
		if plugin.Enabled != nil && !*plugin.Enabled {
			continue
		}

		cfg.Enabled = true

		if v, ok := plugin.Parameters[ParamPoolerPort]; ok {
			if port, err := strconv.Atoi(v); err == nil {
				cfg.PoolerPort = port
			} else {
				cfg.ParseErrors = append(cfg.ParseErrors, fmt.Sprintf("%s: invalid integer %q", ParamPoolerPort, v))
			}
		}
		if v, ok := plugin.Parameters[ParamMetricsPort]; ok {
			if port, err := strconv.Atoi(v); err == nil {
				cfg.MetricsPort = port
			} else {
				cfg.ParseErrors = append(cfg.ParseErrors, fmt.Sprintf("%s: invalid integer %q", ParamMetricsPort, v))
			}
		}
		if v, ok := plugin.Parameters[ParamConfigName]; ok {
			cfg.ConfigName = v
		}

		break
	}

	return cfg
}

func (c *PluginConfiguration) Validate() error {
	if len(c.ParseErrors) > 0 {
		return fmt.Errorf("invalid plugin parameters: %s", strings.Join(c.ParseErrors, "; "))
	}
	if c.ConfigName == "" {
		return fmt.Errorf("%s is required", ParamConfigName)
	}
	if c.PoolerPort <= 0 || c.PoolerPort > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535, got %d", ParamPoolerPort, c.PoolerPort)
	}
	if c.MetricsPort <= 0 || c.MetricsPort > 65535 {
		return fmt.Errorf("%s must be between 1 and 65535, got %d", ParamMetricsPort, c.MetricsPort)
	}
	if c.SidecarImage == "" {
		return fmt.Errorf("SIDECAR_IMAGE environment variable is required")
	}
	if c.PoolerPort == c.MetricsPort {
		return fmt.Errorf("%s and %s must be different", ParamPoolerPort, ParamMetricsPort)
	}
	return nil
}

func SetDefaults(params map[string]string) map[string]string {
	if params == nil {
		params = make(map[string]string)
	}
	if _, ok := params[ParamPoolerPort]; !ok {
		params[ParamPoolerPort] = strconv.Itoa(DefaultPoolerPort)
	}
	if _, ok := params[ParamMetricsPort]; !ok {
		params[ParamMetricsPort] = strconv.Itoa(DefaultMetricsPort)
	}
	return params
}
