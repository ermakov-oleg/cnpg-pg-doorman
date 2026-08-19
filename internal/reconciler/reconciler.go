package reconciler

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/object"
	"github.com/cloudnative-pg/cnpg-i/pkg/reconciler"
	"github.com/cloudnative-pg/machinery/pkg/log"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/config"
	"github.com/o-ermakov/cnpg-pg-doorman/internal/specs"
)

// Implementation implements the ReconcilerHooks service.
type Implementation struct {
	Client client.Client
	reconciler.UnimplementedReconcilerHooksServer
}

// GetCapabilities declares support for Cluster reconciliation.
func (r Implementation) GetCapabilities(
	_ context.Context,
	_ *reconciler.ReconcilerHooksCapabilitiesRequest,
) (*reconciler.ReconcilerHooksCapabilitiesResult, error) {
	return &reconciler.ReconcilerHooksCapabilitiesResult{
		ReconcilerCapabilities: []*reconciler.ReconcilerHooksCapability{
			{Kind: reconciler.ReconcilerHooksCapability_KIND_CLUSTER},
		},
	}, nil
}

// Pre is called before Cluster reconciliation to ensure RBAC.
func (r Implementation) Pre(
	ctx context.Context,
	request *reconciler.ReconcilerHooksRequest,
) (*reconciler.ReconcilerHooksResult, error) {
	kind, err := object.GetKind(request.GetResourceDefinition())
	if err != nil {
		return nil, err
	}
	if kind != "Cluster" {
		return &reconciler.ReconcilerHooksResult{
			Behavior: reconciler.ReconcilerHooksResult_BEHAVIOR_CONTINUE,
		}, nil
	}

	var cluster cnpgv1.Cluster
	if err := decoder.DecodeObjectLenient(request.GetResourceDefinition(), &cluster); err != nil {
		return nil, err
	}

	logger := log.FromContext(ctx).WithValues("cluster", cluster.Name, "namespace", cluster.Namespace)

	pluginConfig := config.NewFromCluster(&cluster)
	if !pluginConfig.Enabled {
		logger.Debug("pg-doorman plugin not enabled, skipping RBAC reconciliation")
		return &reconciler.ReconcilerHooksResult{
			Behavior: reconciler.ReconcilerHooksResult_BEHAVIOR_CONTINUE,
		}, nil
	}
	if err := pluginConfig.Validate(); err != nil {
		// Blocks the whole cluster reconciliation: must be visible at level=error.
		logger.Error(err, "plugin config invalid, blocking reconciliation")
		return nil, err
	}

	// Read PgDoorman CR
	var pgDoorman v1alpha1.PgDoorman
	if err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      pluginConfig.ConfigName,
	}, &pgDoorman); err != nil {
		if apierrs.IsNotFound(err) {
			// Blocks the whole cluster reconciliation until the CR appears.
			logger.Warning("PgDoorman CR not found, requeuing cluster reconciliation",
				"configName", pluginConfig.ConfigName)
			return &reconciler.ReconcilerHooksResult{
				Behavior: reconciler.ReconcilerHooksResult_BEHAVIOR_REQUEUE,
			}, nil
		}
		return nil, err
	}

	if err := r.ensureRole(ctx, &cluster, &pgDoorman); err != nil {
		return nil, err
	}
	if err := r.ensureRoleBinding(ctx, &cluster); err != nil {
		return nil, err
	}

	logger.Debug("Pre hook reconciliation completed")
	return &reconciler.ReconcilerHooksResult{
		Behavior: reconciler.ReconcilerHooksResult_BEHAVIOR_CONTINUE,
	}, nil
}

// Post is a no-op post-reconciliation hook.
func (r Implementation) Post(
	_ context.Context,
	_ *reconciler.ReconcilerHooksRequest,
) (*reconciler.ReconcilerHooksResult, error) {
	return &reconciler.ReconcilerHooksResult{
		Behavior: reconciler.ReconcilerHooksResult_BEHAVIOR_CONTINUE,
	}, nil
}

func (r Implementation) ensureRole(
	ctx context.Context,
	cluster *cnpgv1.Cluster,
	pgDoorman *v1alpha1.PgDoorman,
) error {
	newRole := specs.BuildRole(cluster, pgDoorman)

	var role rbacv1.Role
	if err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: newRole.Namespace,
		Name:      newRole.Name,
	}, &role); err != nil {
		if !apierrs.IsNotFound(err) {
			return err
		}

		log.FromContext(ctx).Info("Creating role", "name", newRole.Name, "namespace", newRole.Namespace)
		if err := ctrl.SetControllerReference(cluster, newRole, r.Client.Scheme()); err != nil {
			return err
		}
		return r.Client.Create(ctx, newRole)
	}

	if equality.Semantic.DeepEqual(newRole.Rules, role.Rules) {
		return nil
	}

	log.FromContext(ctx).Info("Patching role", "name", newRole.Name, "namespace", newRole.Namespace)
	patch := client.MergeFrom(role.DeepCopy())
	role.Rules = newRole.Rules
	return r.Client.Patch(ctx, &role, patch)
}

func (r Implementation) ensureRoleBinding(
	ctx context.Context,
	cluster *cnpgv1.Cluster,
) error {
	newRoleBinding := specs.BuildRoleBinding(cluster)

	var roleBinding rbacv1.RoleBinding
	if err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: cluster.Namespace,
		Name:      specs.GetRBACName(cluster.Name),
	}, &roleBinding); err != nil {
		if !apierrs.IsNotFound(err) {
			return err
		}

		if err := ctrl.SetControllerReference(cluster, newRoleBinding, r.Client.Scheme()); err != nil {
			return err
		}
		return r.Client.Create(ctx, newRoleBinding)
	}

	// RoleRef is immutable — if it changed, delete and recreate
	if !equality.Semantic.DeepEqual(roleBinding.RoleRef, newRoleBinding.RoleRef) {
		log.FromContext(ctx).Info("RoleRef changed, recreating RoleBinding", "name", roleBinding.Name)
		if err := r.Client.Delete(ctx, &roleBinding); err != nil {
			return err
		}
		if err := ctrl.SetControllerReference(cluster, newRoleBinding, r.Client.Scheme()); err != nil {
			return err
		}
		return r.Client.Create(ctx, newRoleBinding)
	}

	// Update subjects if they changed
	if !equality.Semantic.DeepEqual(roleBinding.Subjects, newRoleBinding.Subjects) {
		log.FromContext(ctx).Info("Patching RoleBinding subjects", "name", roleBinding.Name)
		patch := client.MergeFrom(roleBinding.DeepCopy())
		roleBinding.Subjects = newRoleBinding.Subjects
		return r.Client.Patch(ctx, &roleBinding, patch)
	}

	return nil
}
