package operator

import (
	"context"

	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/object"

	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/config"
	"github.com/ermakov-oleg/cnpg-pg-doorman/pkg/metadata"
)

func (i Implementation) MutateCluster(
	_ context.Context,
	request *operator.OperatorMutateClusterRequest,
) (*operator.OperatorMutateClusterResult, error) {
	cluster, err := decoder.DecodeClusterLenient(request.GetDefinition())
	if err != nil {
		return nil, err
	}

	mutatedCluster := cluster.DeepCopy()

	for idx := range mutatedCluster.Spec.Plugins {
		plugin := &mutatedCluster.Spec.Plugins[idx]
		if plugin.Name != metadata.PluginName {
			continue
		}
		plugin.Parameters = config.SetDefaults(plugin.Parameters)
	}

	patch, err := object.CreatePatch(mutatedCluster, cluster)
	if err != nil {
		return nil, err
	}

	return &operator.OperatorMutateClusterResult{
		JsonPatch: patch,
	}, nil
}
