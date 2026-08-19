package operator

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"

	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/config"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/metrics"
)

func (i Implementation) ValidateClusterCreate(
	ctx context.Context,
	request *operator.OperatorValidateClusterCreateRequest,
) (*operator.OperatorValidateClusterCreateResult, error) {
	return metrics.Observe(ctx, "operator.ValidateClusterCreate",
		func() (*operator.OperatorValidateClusterCreateResult, error) {
			return i.validateClusterCreate(ctx, request)
		})
}

func (i Implementation) validateClusterCreate(
	ctx context.Context,
	request *operator.OperatorValidateClusterCreateRequest,
) (*operator.OperatorValidateClusterCreateResult, error) {
	cluster, err := decoder.DecodeClusterLenient(request.GetDefinition())
	if err != nil {
		return nil, err
	}

	cfg := config.NewFromCluster(cluster)
	result := validateConfig(cfg)
	// The CR may legitimately not exist yet at cluster creation (GitOps can
	// apply both at once; the Pre hook holds the reconcile), so only a
	// present-but-mismatching CR is rejected here.
	result.ValidationErrors = append(result.ValidationErrors,
		i.validateClusterRef(ctx, cluster, cfg, false)...)
	return result, nil
}

// validateClusterRef checks that the referenced PgDoorman points back at this
// cluster via spec.clusterRef. requireExists additionally rejects a missing CR
// (used on Change: a running cluster must have its config).
func (i Implementation) validateClusterRef(
	ctx context.Context,
	cluster *cnpgv1.Cluster,
	cfg *config.PluginConfiguration,
	requireExists bool,
) []*operator.ValidationError {
	if i.Client == nil || !cfg.Enabled || cfg.ConfigName == "" {
		return nil
	}

	var pgDoorman v1alpha1.PgDoorman
	err := i.Client.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cfg.ConfigName}, &pgDoorman)
	if err != nil {
		if apierrs.IsNotFound(err) {
			if requireExists {
				return []*operator.ValidationError{{
					PathComponents: []string{"spec", "plugins"},
					Message: fmt.Sprintf("PgDoorman %q not found in namespace %q",
						cfg.ConfigName, cluster.Namespace),
				}}
			}
			return nil
		}
		// Transient read errors must not block admission.
		return nil
	}

	if pgDoorman.Spec.ClusterRef.Name != cluster.Name {
		return []*operator.ValidationError{{
			PathComponents: []string{"spec", "plugins"},
			Message: fmt.Sprintf("PgDoorman %q belongs to cluster %q (spec.clusterRef), not %q",
				cfg.ConfigName, pgDoorman.Spec.ClusterRef.Name, cluster.Name),
		}}
	}
	return nil
}

func (i Implementation) ValidateClusterChange(
	ctx context.Context,
	request *operator.OperatorValidateClusterChangeRequest,
) (*operator.OperatorValidateClusterChangeResult, error) {
	return metrics.Observe(ctx, "operator.ValidateClusterChange",
		func() (*operator.OperatorValidateClusterChangeResult, error) {
			return i.validateClusterChange(ctx, request)
		})
}

func (i Implementation) validateClusterChange(
	ctx context.Context,
	request *operator.OperatorValidateClusterChangeRequest,
) (*operator.OperatorValidateClusterChangeResult, error) {
	cluster, err := decoder.DecodeClusterLenient(request.GetNewCluster())
	if err != nil {
		return nil, err
	}

	cfg := config.NewFromCluster(cluster)
	result := validateConfig(cfg)
	// A running cluster must have its PgDoorman: reject a configName change
	// pointing at a missing or foreign CR instead of freezing the reconcile.
	errs := append(result.ValidationErrors, i.validateClusterRef(ctx, cluster, cfg, true)...)
	return &operator.OperatorValidateClusterChangeResult{
		ValidationErrors: errs,
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
