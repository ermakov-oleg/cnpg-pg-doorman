package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/o-ermakov/cnpg-pg-doorman/pkg/metadata"
)

const (
	DefaultPoolerPort  = 6432
	DefaultMetricsPort = 9127

	// WrapperHealthPort serves the wrapper /healthz probed by the kubelet.
	WrapperHealthPort = 8081

	// Sidecar resource defaults. No CPU limit: pg_doorman runs 4 tokio worker
	// threads by default and a hard CPU cap throttles all pooled traffic.
	DefaultSidecarCPURequest    = "100m"
	DefaultSidecarMemoryRequest = "128Mi"
	DefaultSidecarMemoryLimit   = "512Mi"

	ParamPoolerPort  = "poolerPort"
	ParamMetricsPort = "metricsPort"
	ParamConfigName  = "configName"

	ParamSidecarCPURequest    = "sidecarCpuRequest"
	ParamSidecarMemoryRequest = "sidecarMemoryRequest"
	ParamSidecarCPULimit      = "sidecarCpuLimit"
	ParamSidecarMemoryLimit   = "sidecarMemoryLimit"

	// ResourceNone unsets a defaulted resource value (e.g. no memory limit).
	ResourceNone = "none"
)

type PluginConfiguration struct {
	Enabled      bool
	PoolerPort   int
	MetricsPort  int
	ConfigName   string
	SidecarImage string
	Resources    corev1.ResourceRequirements
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

		cfg.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{},
			Limits:   corev1.ResourceList{},
		}
		cfg.setResource(plugin.Parameters, ParamSidecarCPURequest, cfg.Resources.Requests, corev1.ResourceCPU, DefaultSidecarCPURequest)
		cfg.setResource(plugin.Parameters, ParamSidecarMemoryRequest, cfg.Resources.Requests, corev1.ResourceMemory, DefaultSidecarMemoryRequest)
		cfg.setResource(plugin.Parameters, ParamSidecarCPULimit, cfg.Resources.Limits, corev1.ResourceCPU, "")
		cfg.setResource(plugin.Parameters, ParamSidecarMemoryLimit, cfg.Resources.Limits, corev1.ResourceMemory, DefaultSidecarMemoryLimit)

		break
	}

	return cfg
}

// setResource fills one resource entry from the plugin parameter, falling back
// to defaultVal. Empty default or the explicit "none" value leaves it unset.
func (c *PluginConfiguration) setResource(
	params map[string]string,
	param string,
	list corev1.ResourceList,
	name corev1.ResourceName,
	defaultVal string,
) {
	val := defaultVal
	if v, ok := params[param]; ok {
		val = v
	}
	if val == "" || val == ResourceNone {
		return
	}
	q, err := resource.ParseQuantity(val)
	if err != nil {
		c.ParseErrors = append(c.ParseErrors, fmt.Sprintf("%s: invalid quantity %q", param, val))
		return
	}
	list[name] = q
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
	// Ports already bound in a CNPG instance pod: pg_doorman would fail to
	// bind, the native sidecar would crash-loop and the pod never gets Ready.
	reserved := map[int]string{
		5432:              "PostgreSQL",
		8000:              "CNPG instance manager status port",
		9187:              "CNPG metrics exporter",
		WrapperHealthPort: "wrapper health endpoint",
	}
	for param, port := range map[string]int{ParamPoolerPort: c.PoolerPort, ParamMetricsPort: c.MetricsPort} {
		if owner, ok := reserved[port]; ok {
			return fmt.Errorf("%s %d conflicts with %s", param, port, owner)
		}
	}
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		req, hasReq := c.Resources.Requests[name]
		limit, hasLimit := c.Resources.Limits[name]
		if hasReq && hasLimit && req.Cmp(limit) > 0 {
			return fmt.Errorf("sidecar %s request %s exceeds limit %s", name, req.String(), limit.String())
		}
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
