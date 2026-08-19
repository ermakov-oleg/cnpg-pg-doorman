package lifecycle

import (
	"testing"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/o-ermakov/cnpg-pg-doorman/internal/config"
)

func testCluster() *cnpgv1.Cluster {
	cluster := &cnpgv1.Cluster{}
	cluster.Name = "cluster"
	cluster.Namespace = "ns"
	return cluster
}

func testPluginConfig() *config.PluginConfiguration {
	return &config.PluginConfiguration{
		ConfigName:   "test-doorman",
		SidecarImage: "wrapper:test",
		PoolerPort:   6432,
		MetricsPort:  9127,
	}
}

func findSidecar(t *testing.T, spec *corev1.PodSpec) *corev1.Container {
	t.Helper()
	for i := range spec.InitContainers {
		if spec.InitContainers[i].Name == sidecarContainerName {
			return &spec.InitContainers[i]
		}
	}
	t.Fatalf("sidecar container %q not found in init containers", sidecarContainerName)
	return nil
}

func TestInjectSidecarNoReadinessProbe(t *testing.T) {
	// A broken pooler must not make the whole PostgreSQL pod NotReady:
	// native sidecar readiness gates pod readiness, which would drop a
	// healthy PostgreSQL from all Service endpoints.
	spec := &corev1.PodSpec{}
	injectSidecar(spec, testPluginConfig(), testCluster())

	sidecar := findSidecar(t, spec)
	if sidecar.ReadinessProbe != nil {
		t.Errorf("sidecar must not have a readiness probe, got %+v", sidecar.ReadinessProbe)
	}
}

func TestInjectSidecarLivenessProbesWrapperNotPooler(t *testing.T) {
	// Liveness must target the wrapper health endpoint: a TCP probe on the
	// pooler port kills the container while the wrapper legitimately waits
	// for its config (missing CR/Secret), causing CrashLoopBackOff.
	spec := &corev1.PodSpec{}
	injectSidecar(spec, testPluginConfig(), testCluster())

	sidecar := findSidecar(t, spec)
	if sidecar.LivenessProbe == nil {
		t.Fatal("sidecar must have a liveness probe")
	}
	httpGet := sidecar.LivenessProbe.HTTPGet
	if httpGet == nil {
		t.Fatalf("liveness probe must be an HTTP probe on the wrapper, got %+v", sidecar.LivenessProbe.ProbeHandler)
	}
	if httpGet.Path != "/healthz" {
		t.Errorf("liveness probe path = %q, want /healthz", httpGet.Path)
	}
	if got := httpGet.Port.IntValue(); got != config.WrapperHealthPort {
		t.Errorf("liveness probe port = %d, want %d", got, config.WrapperHealthPort)
	}
}

func TestInjectSidecarNoStartupProbe(t *testing.T) {
	// A startup probe on a native sidecar blocks the start of subsequent
	// containers (PostgreSQL) until it succeeds — never add one.
	spec := &corev1.PodSpec{}
	injectSidecar(spec, testPluginConfig(), testCluster())

	sidecar := findSidecar(t, spec)
	if sidecar.StartupProbe != nil {
		t.Errorf("sidecar must not have a startup probe, got %+v", sidecar.StartupProbe)
	}
}

func TestInjectSidecarHealthPortEnv(t *testing.T) {
	spec := &corev1.PodSpec{}
	injectSidecar(spec, testPluginConfig(), testCluster())

	sidecar := findSidecar(t, spec)
	for _, env := range sidecar.Env {
		if env.Name == "HEALTH_PORT" {
			return
		}
	}
	t.Error("sidecar must have HEALTH_PORT env var so the wrapper binds the probed port")
}

func TestInjectSidecarIdempotent(t *testing.T) {
	spec := &corev1.PodSpec{}
	injectSidecar(spec, testPluginConfig(), testCluster())
	injectSidecar(spec, testPluginConfig(), testCluster())

	if got := len(spec.InitContainers); got != 1 {
		t.Errorf("expected 1 init container after double injection, got %d", got)
	}
	if got := len(spec.Volumes); got != 2 {
		t.Errorf("expected 2 volumes (scratch+tls) after double injection, got %d", got)
	}
}

func TestInjectSidecarMountsServerTLS(t *testing.T) {
	spec := &corev1.PodSpec{}
	injectSidecar(spec, testPluginConfig(), testCluster())

	var tlsVolume *corev1.Volume
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == tlsVolumeName {
			tlsVolume = &spec.Volumes[i]
		}
	}
	if tlsVolume == nil {
		t.Fatal("pod must have the server TLS volume")
	}
	if tlsVolume.Secret == nil || tlsVolume.Secret.SecretName != "cluster-server" {
		t.Errorf("TLS volume must reference the cluster server secret, got %+v", tlsVolume.VolumeSource)
	}

	sidecar := findSidecar(t, spec)
	var mounted bool
	for _, m := range sidecar.VolumeMounts {
		if m.Name == tlsVolumeName {
			mounted = true
			if !m.ReadOnly {
				t.Error("TLS mount must be read-only")
			}
		}
	}
	if !mounted {
		t.Fatal("sidecar must mount the server TLS volume")
	}

	env := map[string]string{}
	for _, e := range sidecar.Env {
		env[e.Name] = e.Value
	}
	if env["TLS_CERT_PATH"] != tlsMountPath+"/tls.crt" {
		t.Errorf("TLS_CERT_PATH = %q", env["TLS_CERT_PATH"])
	}
	if env["TLS_KEY_PATH"] != tlsMountPath+"/tls.key" {
		t.Errorf("TLS_KEY_PATH = %q", env["TLS_KEY_PATH"])
	}
}

func TestServerTLSSecretNameOverride(t *testing.T) {
	cluster := &cnpgv1.Cluster{}
	cluster.Name = "my-cluster"
	if got := serverTLSSecretName(cluster); got != "my-cluster-server" {
		t.Errorf("default secret name = %q, want my-cluster-server", got)
	}
	cluster.Spec.Certificates = &cnpgv1.CertificatesConfiguration{ServerTLSSecret: "custom-tls"}
	if got := serverTLSSecretName(cluster); got != "custom-tls" {
		t.Errorf("overridden secret name = %q, want custom-tls", got)
	}
}

func TestInjectSidecarResourcesFromConfig(t *testing.T) {
	cfg := testPluginConfig()
	cfg.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("256Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("1Gi")},
	}
	spec := &corev1.PodSpec{}
	injectSidecar(spec, cfg, testCluster())

	sidecar := findSidecar(t, spec)
	if got := sidecar.Resources.Limits.Memory().String(); got != "1Gi" {
		t.Errorf("memory limit = %s, want 1Gi (must come from plugin config)", got)
	}
	if got := sidecar.Resources.Requests.Memory().String(); got != "256Mi" {
		t.Errorf("memory request = %s, want 256Mi", got)
	}
}
