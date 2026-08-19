package operator

import (
	"context"

	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"

	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/config"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/metrics"
)

func (i Implementation) ValidateClusterCreate(
	ctx context.Context,
	request *operator.OperatorValidateClusterCreateRequest,
) (*operator.OperatorValidateClusterCreateResult, error) {
	return metrics.Observe(ctx, "operator.ValidateClusterCreate",
		func() (*operator.OperatorValidateClusterCreateResult, error) {
			return i.validateClusterCreate(request)
		})
}

func (i Implementation) validateClusterCreate(
	request *operator.OperatorValidateClusterCreateRequest,
) (*operator.OperatorValidateClusterCreateResult, error) {
	cluster, err := decoder.DecodeClusterLenient(request.GetDefinition())
	if err != nil {
		return nil, err
	}

	cfg := config.NewFromCluster(cluster)
	return validateConfig(cfg), nil
}

func (i Implementation) ValidateClusterChange(
	ctx context.Context,
	request *operator.OperatorValidateClusterChangeRequest,
) (*operator.OperatorValidateClusterChangeResult, error) {
	return metrics.Observe(ctx, "operator.ValidateClusterChange",
		func() (*operator.OperatorValidateClusterChangeResult, error) {
			return i.validateClusterChange(request)
		})
}

func (i Implementation) validateClusterChange(
	request *operator.OperatorValidateClusterChangeRequest,
) (*operator.OperatorValidateClusterChangeResult, error) {
	cluster, err := decoder.DecodeClusterLenient(request.GetNewCluster())
	if err != nil {
		return nil, err
	}

	cfg := config.NewFromCluster(cluster)
	result := validateConfig(cfg)
	return &operator.OperatorValidateClusterChangeResult{
		ValidationErrors: result.ValidationErrors,
	}, nil
}

func validateConfig(cfg *config.PluginConfiguration) *operator.OperatorValidateClusterCreateResult {
	if !cfg.Enabled {
		return &operator.OperatorValidateClusterCreateResult{}
	}

	var errs []*operator.ValidationError

	if err := cfg.Validate(); err != nil {
		errs = append(errs, &operator.ValidationError{
			PathComponents: []string{"spec", "plugins"},
			Message:        err.Error(),
		})
	}

	return &operator.OperatorValidateClusterCreateResult{
		ValidationErrors: errs,
	}
}
