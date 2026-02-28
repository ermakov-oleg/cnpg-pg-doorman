//go:build e2e

package cluster

import (
	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
)

func IsReady(cluster cnpgv1.Cluster) bool {
	return cluster.Status.Instances == cluster.Spec.Instances &&
		cluster.Status.ReadyInstances == cluster.Spec.Instances
}
