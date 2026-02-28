package operator

import (
	"context"

	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
)

type Implementation struct {
	operator.UnimplementedOperatorServer
}

func (i Implementation) GetCapabilities(
	_ context.Context,
	_ *operator.OperatorCapabilitiesRequest,
) (*operator.OperatorCapabilitiesResult, error) {
	return &operator.OperatorCapabilitiesResult{
		Capabilities: []*operator.OperatorCapability{
			{
				Type: &operator.OperatorCapability_Rpc{
					Rpc: &operator.OperatorCapability_RPC{
						Type: operator.OperatorCapability_RPC_TYPE_VALIDATE_CLUSTER_CREATE,
					},
				},
			},
			{
				Type: &operator.OperatorCapability_Rpc{
					Rpc: &operator.OperatorCapability_RPC{
						Type: operator.OperatorCapability_RPC_TYPE_VALIDATE_CLUSTER_CHANGE,
					},
				},
			},
			{
				Type: &operator.OperatorCapability_Rpc{
					Rpc: &operator.OperatorCapability_RPC{
						Type: operator.OperatorCapability_RPC_TYPE_MUTATE_CLUSTER,
					},
				},
			},
		},
	}, nil
}
