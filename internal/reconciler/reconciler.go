package reconciler

import (
	"context"
	"fmt"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/decoder"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/object"
	"github.com/cloudnative-pg/cnpg-i/pkg/reconciler"
	"github.com/cloudnative-pg/machinery/pkg/log"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/config"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/controller"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/metrics"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/specs"
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
	return metrics.Observe(ctx, "reconciler.Pre", func() (*reconciler.ReconcilerHooksResult, error) {
		return r.pre(ctx, request)
	})
}

func (r Implementation) pre(
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
		// The plugin was disabled or removed from spec.plugins: without a
		// cleanup the per-cluster Role keeps granting the instance
		// ServiceAccount access to password secrets.
		if err := r.cleanup(ctx, &cluster); err != nil {
			return nil, err
		}
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

	if pgDoorman.Spec.ClusterRef.Name != cluster.Name {
		err := fmt.Errorf("PgDoorman %q belongs to cluster %q (spec.clusterRef), not %q",
			pgDoorman.Name, pgDoorman.Spec.ClusterRef.Name, cluster.Name)
		logger.Error(err, "refusing to reconcile with a foreign PgDoorman")
		return nil, err
	}

	// RBAC for pods is gone: config is rendered centrally into a Secret.
	// Legacy per-cluster Role/RoleBinding are removed on sight.
	if err := r.cleanupLegacyRBAC(ctx, &cluster); err != nil {
		return nil, err
	}
	if err := r.ensureService(ctx, &cluster, pluginConfig.PoolerPort); err != nil {
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

// cleanup removes the plugin-managed resources when the plugin is disabled.
func (r Implementation) cleanup(ctx context.Context, cluster *cnpgv1.Cluster) error {
	logger := log.FromContext(ctx).WithValues("cluster", cluster.Name, "namespace", cluster.Namespace)

	objects := []client.Object{
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: specs.GetRBACName(cluster.Name),
		}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: specs.GetRBACName(cluster.Name),
		}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: specs.GetServiceName(cluster.Name),
		}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: controller.RenderedSecretName(cluster.Name),
		}},
	}
	for _, obj := range objects {
		if err := r.Client.Delete(ctx, obj); err != nil {
			if apierrs.IsNotFound(err) {
				continue
			}
			return err
		}
		logger.Info("Deleted plugin resource after plugin was disabled",
			"kind", obj.GetObjectKind().GroupVersionKind().Kind, "name", obj.GetName())
	}
	return nil
}

// cleanupLegacyRBAC removes the pre-rendered-config per-cluster Role and
// RoleBinding that granted pods direct secret access.
func (r Implementation) cleanupLegacyRBAC(ctx context.Context, cluster *cnpgv1.Cluster) error {
	for _, obj := range []client.Object{
		&rbacv1.Role{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: specs.GetRBACName(cluster.Name),
		}},
		&rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace, Name: specs.GetRBACName(cluster.Name),
		}},
	} {
		if err := r.Client.Delete(ctx, obj); err != nil && !apierrs.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r Implementation) ensureService(
	ctx context.Context,
	cluster *cnpgv1.Cluster,
	poolerPort int,
) error {
	newService := specs.BuildDoormanService(cluster, poolerPort)

	var svc corev1.Service
	if err := r.Client.Get(ctx, client.ObjectKey{
		Namespace: newService.Namespace,
		Name:      newService.Name,
	}, &svc); err != nil {
		if !apierrs.IsNotFound(err) {
			return err
		}

		log.FromContext(ctx).Info("Creating pooler service", "name", newService.Name, "namespace", newService.Namespace)
		if err := ctrl.SetControllerReference(cluster, newService, r.Client.Scheme()); err != nil {
			return err
		}
		return r.Client.Create(ctx, newService)
	}

	if equality.Semantic.DeepEqual(svc.Spec.Selector, newService.Spec.Selector) &&
		equality.Semantic.DeepEqual(svc.Spec.Ports, newService.Spec.Ports) {
		return nil
	}

	log.FromContext(ctx).Info("Patching pooler service", "name", newService.Name, "namespace", newService.Namespace)
	patch := client.MergeFrom(svc.DeepCopy())
	svc.Spec.Selector = newService.Spec.Selector
	svc.Spec.Ports = newService.Spec.Ports
	return r.Client.Patch(ctx, &svc, patch)
}

