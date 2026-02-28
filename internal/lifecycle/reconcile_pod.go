package lifecycle

import (
	"fmt"
	"strconv"

	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/object"

	"github.com/o-ermakov/cnpg-pg-doorman/internal/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	sidecarContainerName = "pg-doorman"
	scratchVolumeName    = "pg-doorman-scratch"
	scratchMountPath     = "/tmp"
)

func reconcilePod(
	request *lifecycle.OperatorLifecycleRequest,
	cfg *config.PluginConfiguration,
	clusterName, clusterNamespace string,
) (*lifecycle.OperatorLifecycleResponse, error) {
	pod, err := decoder.DecodePodJSON(request.GetObjectDefinition())
	if err != nil {
		return nil, fmt.Errorf("cannot decode pod: %w", err)
	}

	mutatedPod := pod.DeepCopy()
	injectSidecar(&mutatedPod.Spec, cfg, clusterName, clusterNamespace)

	patch, err := object.CreatePatch(mutatedPod, pod)
	if err != nil {
		return nil, fmt.Errorf("cannot create patch: %w", err)
	}

	return &lifecycle.OperatorLifecycleResponse{
		JsonPatch: patch,
	}, nil
}

func injectSidecar(spec *corev1.PodSpec, cfg *config.PluginConfiguration, clusterName, clusterNamespace string) {
	// Add emptyDir scratch volume for /tmp
	if !hasVolume(spec.Volumes, scratchVolumeName) {
		spec.Volumes = append(spec.Volumes, corev1.Volume{
			Name: scratchVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}

	// Build sidecar container
	sidecar := corev1.Container{
		Name:  sidecarContainerName,
		Image: cfg.SidecarImage,
		Ports: []corev1.ContainerPort{
			{
				Name:          "pooler",
				ContainerPort: int32(cfg.PoolerPort),
				Protocol:      corev1.ProtocolTCP,
			},
			{
				Name:          "metrics",
				ContainerPort: int32(cfg.MetricsPort),
				Protocol:      corev1.ProtocolTCP,
			},
		},
		Env: []corev1.EnvVar{
			{Name: "PG_DOORMAN_CONFIG_NAME", Value: cfg.ConfigName},
			{Name: "PG_DOORMAN_CONFIG_NAMESPACE", Value: clusterNamespace},
			{Name: "POOLER_PORT", Value: strconv.Itoa(cfg.PoolerPort)},
			{Name: "METRICS_PORT", Value: strconv.Itoa(cfg.MetricsPort)},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      scratchVolumeName,
				MountPath: scratchMountPath,
			},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("500m"),
				corev1.ResourceMemory: resource.MustParse("256Mi"),
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt32(int32(cfg.PoolerPort)),
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       10,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt32(int32(cfg.PoolerPort)),
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       30,
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:             ptr.To(true),
			AllowPrivilegeEscalation: ptr.To(false),
			ReadOnlyRootFilesystem:   ptr.To(true),
			Privileged:               ptr.To(false),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		},
		RestartPolicy: ptr.To(corev1.ContainerRestartPolicyAlways),
	}

	// Inject as init container (sidecar pattern for K8s 1.29+)
	for i := range spec.InitContainers {
		if spec.InitContainers[i].Name == sidecarContainerName {
			spec.InitContainers[i] = sidecar
			return
		}
	}
	spec.InitContainers = append(spec.InitContainers, sidecar)
}

func hasVolume(volumes []corev1.Volume, name string) bool {
	for i := range volumes {
		if volumes[i].Name == name {
			return true
		}
	}
	return false
}
