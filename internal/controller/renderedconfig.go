// Package controller renders pg_doorman configuration into per-cluster
// Secrets. Rendering happens centrally (leader-elected) so PostgreSQL pods
// need zero RBAC: the sidecar only mounts the rendered Secret.
package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/config"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/configgen"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/credentials"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/specs"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/wrapper"
)

const (
	// ConfigKey is the rendered config file name inside the Secret.
	ConfigKey = "pg_doorman.yaml"
	// generatedAdminPasswordKey persists the generated admin password across
	// re-renders so reloads do not churn it.
	generatedAdminPasswordKey = "admin-password"

	// ClusterLabel marks secrets as belonging to a cluster (set by CNPG on
	// its own secrets; users must set it on custom referenced ones). It both
	// authorizes the reference (confused-deputy guard) and scopes the Secret
	// informer, so rotation triggers an immediate re-render.
	ClusterLabel = "cnpg.io/cluster"

	requeueAfter = 30 * time.Second
)

// RenderedSecretName returns the per-cluster rendered config Secret name.
func RenderedSecretName(clusterName string) string {
	return clusterName + "-doorman-config"
}

// RenderedConfigReconciler renders PgDoorman specs into per-cluster Secrets.
type RenderedConfigReconciler struct {
	client.Client
	Recorder record.EventRecorder
}

// Reconcile renders the config for one PgDoorman.
func (r *RenderedConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pgDoorman v1alpha1.PgDoorman
	if err := r.Get(ctx, req.NamespacedName, &pgDoorman); err != nil {
		// Deleted CR: the rendered Secret intentionally stays (ownerReference
		// on the Cluster) so running poolers keep their last-good config.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var cluster cnpgv1.Cluster
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: pgDoorman.Namespace,
		Name:      pgDoorman.Spec.ClusterRef.Name,
	}, &cluster); err != nil {
		if apierrs.IsNotFound(err) {
			// The Cluster may not exist yet (GitOps ordering).
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		return ctrl.Result{}, err
	}

	cfg := config.NewFromCluster(&cluster)
	if !cfg.Enabled || cfg.ConfigName != pgDoorman.Name {
		// The cluster does not (or no longer does) use this CR.
		return ctrl.Result{}, nil
	}
	if err := cfg.Validate(); err != nil {
		r.eventf(&pgDoorman, "InvalidPluginConfig", "plugin parameters invalid: %v", err)
		return ctrl.Result{}, nil
	}

	if err := r.validateSecretOwnership(ctx, &pgDoorman, cluster.Name); err != nil {
		r.eventf(&pgDoorman, "SecretNotAllowed", "%v", err)
		logger.Error(err, "refusing to render config", "cluster", cluster.Name)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	data, generatedAdmin, err := r.render(ctx, &pgDoorman, &cluster, cfg)
	if err != nil {
		r.eventf(&pgDoorman, "RenderFailed", "%v", err)
		logger.Error(err, "config render failed", "cluster", cluster.Name)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	if err := r.upsertSecret(ctx, &cluster, data, generatedAdmin); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// validateSecretOwnership rejects references to secrets not labeled as
// belonging to the cluster: otherwise anyone able to edit a PgDoorman could
// leak arbitrary namespace secrets into the pooler config (confused deputy).
// Note: the Secret informer is scoped by the same label, so an unlabeled
// secret reads as NotFound here.
func (r *RenderedConfigReconciler) validateSecretOwnership(
	ctx context.Context,
	pgDoorman *v1alpha1.PgDoorman,
	clusterName string,
) error {
	for _, name := range specs.CollectSecretNames(&pgDoorman.Spec) {
		var secret corev1.Secret
		if err := r.Get(ctx, types.NamespacedName{Namespace: pgDoorman.Namespace, Name: name}, &secret); err != nil {
			if apierrs.IsNotFound(err) {
				return fmt.Errorf(
					"referenced secret %q not found or not labeled %s=%s",
					name, ClusterLabel, clusterName)
			}
			return fmt.Errorf("referenced secret %q: %w", name, err)
		}
		if secret.Labels[ClusterLabel] != clusterName {
			return fmt.Errorf(
				"referenced secret %q is not labeled %s=%s",
				name, ClusterLabel, clusterName)
		}
	}
	return nil
}

func (r *RenderedConfigReconciler) render(
	ctx context.Context,
	pgDoorman *v1alpha1.PgDoorman,
	cluster *cnpgv1.Cluster,
	cfg *config.PluginConfiguration,
) (data []byte, generatedAdmin string, err error) {
	passwords, err := credentials.ResolvePasswords(ctx, r.Client, pgDoorman.Namespace, &pgDoorman.Spec)
	if err != nil {
		return nil, "", err
	}

	generatedAdmin, err = r.adminPasswordFallback(ctx, cluster)
	if err != nil {
		return nil, "", err
	}
	passwords = configgen.EnsureAdminPassword(passwords, generatedAdmin)

	data, err = configgen.Generate(&pgDoorman.Spec, cfg.PoolerPort, cfg.MetricsPort, passwords, &configgen.TLSFiles{
		Certificate: wrapper.TLSCertPath,
		PrivateKey:  wrapper.ConvertedTLSKeyPath,
	})
	if err != nil {
		return nil, "", err
	}
	if _, err := wrapper.ValidateConfigBytes(data); err != nil {
		return nil, "", err
	}
	return data, generatedAdmin, nil
}

// adminPasswordFallback returns a random admin password, stable across
// re-renders: it is persisted in the rendered Secret and reused.
func (r *RenderedConfigReconciler) adminPasswordFallback(
	ctx context.Context,
	cluster *cnpgv1.Cluster,
) (string, error) {
	var existing corev1.Secret
	err := r.Get(ctx, types.NamespacedName{
		Namespace: cluster.Namespace,
		Name:      RenderedSecretName(cluster.Name),
	}, &existing)
	if err == nil {
		if prev, ok := existing.Data[generatedAdminPasswordKey]; ok && len(prev) > 0 {
			return string(prev), nil
		}
	} else if !apierrs.IsNotFound(err) {
		return "", err
	}

	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func (r *RenderedConfigReconciler) upsertSecret(
	ctx context.Context,
	cluster *cnpgv1.Cluster,
	data []byte,
	generatedAdmin string,
) error {
	logger := log.FromContext(ctx)

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Namespace,
			Name:      RenderedSecretName(cluster.Name),
			Labels: map[string]string{
				ClusterLabel: cluster.Name,
			},
		},
		Data: map[string][]byte{
			ConfigKey:                 data,
			generatedAdminPasswordKey: []byte(generatedAdmin),
		},
	}

	var existing corev1.Secret
	if err := r.Get(ctx, client.ObjectKeyFromObject(desired), &existing); err != nil {
		if !apierrs.IsNotFound(err) {
			return err
		}
		if err := ctrl.SetControllerReference(cluster, desired, r.Scheme()); err != nil {
			return err
		}
		logger.Info("creating rendered config secret", "name", desired.Name, "namespace", desired.Namespace)
		return r.Create(ctx, desired)
	}

	if string(existing.Data[ConfigKey]) == string(data) &&
		string(existing.Data[generatedAdminPasswordKey]) == generatedAdmin {
		return nil
	}

	logger.Info("updating rendered config secret", "name", desired.Name, "namespace", desired.Namespace)
	patch := client.MergeFrom(existing.DeepCopy())
	existing.Data = desired.Data
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	existing.Labels[ClusterLabel] = cluster.Name
	return r.Patch(ctx, &existing, patch)
}

func (r *RenderedConfigReconciler) eventf(obj client.Object, reason, format string, args ...any) {
	if r.Recorder != nil {
		r.Recorder.Eventf(obj, corev1.EventTypeWarning, reason, format, args...)
	}
}

// SetupWithManager wires the controller: reconciles on PgDoorman changes,
// on Cluster changes (plugin parameters carry ports and the configName), and
// on referenced Secret changes (password rotation re-renders immediately).
func (r *RenderedConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	mapCluster := func(ctx context.Context, obj client.Object) []ctrl.Request {
		cluster, ok := obj.(*cnpgv1.Cluster)
		if !ok {
			return nil
		}
		cfg := config.NewFromCluster(cluster)
		if !cfg.Enabled || cfg.ConfigName == "" {
			return nil
		}
		return []ctrl.Request{{NamespacedName: types.NamespacedName{
			Namespace: cluster.Namespace, Name: cfg.ConfigName,
		}}}
	}

	mapSecret := func(ctx context.Context, obj client.Object) []ctrl.Request {
		var list v1alpha1.PgDoormanList
		if err := r.List(ctx, &list, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		var reqs []ctrl.Request
		for i := range list.Items {
			for _, name := range specs.CollectSecretNames(&list.Items[i].Spec) {
				if name == obj.GetName() {
					reqs = append(reqs, ctrl.Request{NamespacedName: types.NamespacedName{
						Namespace: list.Items[i].Namespace, Name: list.Items[i].Name,
					}})
					break
				}
			}
		}
		return reqs
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.PgDoorman{}).
		Watches(&cnpgv1.Cluster{}, handler.EnqueueRequestsFromMapFunc(mapCluster)).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(mapSecret)).
		Complete(r)
}
