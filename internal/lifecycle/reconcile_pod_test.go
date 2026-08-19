package lifecycle

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/o-ermakov/cnpg-pg-doorman/internal/config"
)

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
	injectSidecar(spec, testPluginConfig(), "cluster", "ns")

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
	injectSidecar(spec, testPluginConfig(), "cluster", "ns")

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
	if got := httpGet.Port.IntValue(); got != wrapperHealthPort {
		t.Errorf("liveness probe port = %d, want %d", got, wrapperHealthPort)
	}
}

func TestInjectSidecarNoStartupProbe(t *testing.T) {
	// A startup probe on a native sidecar blocks the start of subsequent
	// containers (PostgreSQL) until it succeeds — never add one.
	spec := &corev1.PodSpec{}
	injectSidecar(spec, testPluginConfig(), "cluster", "ns")

	sidecar := findSidecar(t, spec)
	if sidecar.StartupProbe != nil {
		t.Errorf("sidecar must not have a startup probe, got %+v", sidecar.StartupProbe)
	}
}

func TestInjectSidecarHealthPortEnv(t *testing.T) {
	spec := &corev1.PodSpec{}
	injectSidecar(spec, testPluginConfig(), "cluster", "ns")

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
	injectSidecar(spec, testPluginConfig(), "cluster", "ns")
	injectSidecar(spec, testPluginConfig(), "cluster", "ns")

	if got := len(spec.InitContainers); got != 1 {
		t.Errorf("expected 1 init container after double injection, got %d", got)
	}
	if got := len(spec.Volumes); got != 1 {
		t.Errorf("expected 1 volume after double injection, got %d", got)
	}
}
