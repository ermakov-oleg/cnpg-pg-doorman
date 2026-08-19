package specs

import (
	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// GetServiceName returns the pooler read-write Service name for a cluster.
func GetServiceName(clusterName string) string {
	return clusterName + "-doorman-rw"
}

// BuildDoormanService creates the read-write Service routing to pg_doorman on
// the primary instance: the standard CNPG -rw Service points at 5432 past the
// pooler, so without this every user hand-rolls the same manifest.
func BuildDoormanService(cluster *cnpgv1.Cluster, poolerPort int) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      GetServiceName(cluster.Name),
			Labels: map[string]string{
				"cnpg.io/cluster": cluster.Name,
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"cnpg.io/cluster":      cluster.Name,
				"cnpg.io/instanceRole": "primary",
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "pooler",
					Port:       5432,
					TargetPort: intstr.FromInt32(int32(poolerPort)), //nolint:gosec // validated to 1..65535
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}
