package lifecycle

import (
	"context"
	"fmt"

	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/object"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/machinery/pkg/log"

	"github.com/o-ermakov/cnpg-pg-doorman/internal/config"
)

type Implementation struct {
	lifecycle.UnimplementedOperatorLifecycleServer
}

func (impl Implementation) GetCapabilities(
	_ context.Context,
	_ *lifecycle.OperatorLifecycleCapabilitiesRequest,
) (*lifecycle.OperatorLifecycleCapabilitiesResponse, error) {
	return &lifecycle.OperatorLifecycleCapabilitiesResponse{
		LifecycleCapabilities: []*lifecycle.OperatorLifecycleCapabilities{
			{
				Group: "",
				Kind:  "Pod",
				OperationTypes: []*lifecycle.OperatorOperationType{
					{Type: lifecycle.OperatorOperationType_TYPE_CREATE},
					{Type: lifecycle.OperatorOperationType_TYPE_EVALUATE},
				},
			},
		},
	}, nil
}

func (impl Implementation) LifecycleHook(
	ctx context.Context,
	request *lifecycle.OperatorLifecycleRequest,
) (*lifecycle.OperatorLifecycleResponse, error) {
	kind, err := object.GetKind(request.GetObjectDefinition())
	if err != nil {
		return nil, fmt.Errorf("cannot get object kind: %w", err)
	}

	cluster, err := decoder.DecodeClusterLenient(request.GetClusterDefinition())
	if err != nil {
		return nil, fmt.Errorf("cannot decode cluster: %w", err)
	}

	pluginConfig := config.NewFromCluster(cluster)
	if !pluginConfig.Enabled {
		return &lifecycle.OperatorLifecycleResponse{}, nil
	}
	if err := pluginConfig.Validate(); err != nil {
		// Blocks pod creation for the cluster: must be visible at level=error.
		log.FromContext(ctx).Error(err, "plugin config invalid, blocking lifecycle",
			"cluster", cluster.Name, "namespace", cluster.Namespace)
		return nil, err
	}

	switch kind {
	case "Pod":
		return reconcilePod(request, pluginConfig, cluster)
	default:
		return &lifecycle.OperatorLifecycleResponse{}, nil
	}
}
