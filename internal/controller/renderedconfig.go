// Package controller renders pg_doorman configuration into per-cluster
// Secrets. Rendering happens centrally (leader-elected) so PostgreSQL pods
// need zero RBAC: the sidecar only mounts the rendered Secret.
package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	apierrs "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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

// Finalizer blocks deletion of a PgDoorman still referenced by a live
// Cluster. Managed by the leader-elected controller: a single writer, so no
// races between instances.
const Finalizer = "pg-doorman.cnpg.io/in-use"

// RenderedSecretName returns the per-cluster rendered config Secret name.
func RenderedSecretName(clusterName string) string {
	return clusterName + "-doorman-config"
}

// RenderedConfigReconciler renders PgDoorman specs into per-cluster Secrets.
type RenderedConfigReconciler struct {
	client.Client
	Recorder record.EventRecorder
	// Binary is the desired pg_doorman binary published to wrappers via the
	// rendered Secret; nil disables in-place binary delivery.
	Binary *wrapper.BinarySpec
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
	clusterExists := true
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: pgDoorman.Namespace,
		Name:      pgDoorman.Spec.ClusterRef.Name,
	}, &cluster); err != nil {
		if !apierrs.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		clusterExists = false
	}

	inUse := clusterExists && config.NewFromCluster(&cluster).ConfigName == pgDoorman.Name

	if !pgDoorman.DeletionTimestamp.IsZero() {
		return r.reconcileDeletion(ctx, &pgDoorman, inUse)
	}
	if err := r.ensureFinalizer(ctx, &pgDoorman, inUse); err != nil {
		return ctrl.Result{}, err
	}

	if !clusterExists {
		// The Cluster may not exist yet (GitOps ordering).
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	cfg := config.NewFromCluster(&cluster)
	if !cfg.Enabled || cfg.ConfigName != pgDoorman.Name {
		// The cluster does not (or no longer does) use this CR.
		return ctrl.Result{}, nil
	}
	if err := cfg.Validate(); err != nil {
		r.eventf(&pgDoorman, "InvalidPluginConfig", "plugin parameters invalid: %v", err)
		return ctrl.Result{}, r.setRendered(ctx, &pgDoorman, false, "InvalidPluginConfig", err.Error())
	}

	if err := r.validateSecretOwnership(ctx, &pgDoorman, cluster.Name); err != nil {
		r.eventf(&pgDoorman, "SecretNotAllowed", "%v", err)
		logger.Error(err, "refusing to render config", "cluster", cluster.Name)
		if statusErr := r.setRendered(ctx, &pgDoorman, false, "SecretNotAllowed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	data, generatedAdmin, err := r.render(ctx, &pgDoorman, &cluster, cfg)
	if err != nil {
		r.eventf(&pgDoorman, "RenderFailed", "%v", err)
		logger.Error(err, "config render failed", "cluster", cluster.Name)
		if statusErr := r.setRendered(ctx, &pgDoorman, false, "RenderFailed", err.Error()); statusErr != nil {
			return ctrl.Result{}, statusErr
		}
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}

	if err := r.upsertSecret(ctx, &cluster, data, generatedAdmin, cfg.InPlaceUpgrades); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, r.setRendered(ctx, &pgDoorman, true, "Rendered",
		fmt.Sprintf("generation %d rendered into secret %s", pgDoorman.Generation, RenderedSecretName(cluster.Name)))
}

// setRendered updates the Rendered condition and observedGeneration; a
// False->True transition additionally emits a Normal recovery event.
func (r *RenderedConfigReconciler) setRendered(
	ctx context.Context,
	pgDoorman *v1alpha1.PgDoorman,
	ok bool,
	reason, message string,
) error {
	status := metav1.ConditionFalse
	if ok {
		status = metav1.ConditionTrue
	}

	prev := meta.FindStatusCondition(pgDoorman.Status.Conditions, "Rendered")
	changed := meta.SetStatusCondition(&pgDoorman.Status.Conditions, metav1.Condition{
		Type:               "Rendered",
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: pgDoorman.Generation,
	})
	if ok {
		pgDoorman.Status.ObservedGeneration = pgDoorman.Generation
	}
	if !changed && pgDoorman.Status.ObservedGeneration == pgDoorman.Generation {
		return nil
	}
	if ok && prev != nil && prev.Status == metav1.ConditionFalse && r.Recorder != nil {
		r.Recorder.Eventf(pgDoorman, corev1.EventTypeNormal, "Rendered", "%s", message)
	}
	return r.Status().Update(ctx, pgDoorman)
}

// reconcileDeletion releases the finalizer only when no live Cluster
// references the CR anymore; otherwise the deletion stays blocked.
func (r *RenderedConfigReconciler) reconcileDeletion(
	ctx context.Context,
	pgDoorman *v1alpha1.PgDoorman,
	inUse bool,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(pgDoorman, Finalizer) {
		return ctrl.Result{}, nil
	}
	if inUse {
		r.eventf(pgDoorman, "DeletionBlocked",
			"PgDoorman is still referenced by cluster %q", pgDoorman.Spec.ClusterRef.Name)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	patch := client.MergeFrom(pgDoorman.DeepCopy())
	controllerutil.RemoveFinalizer(pgDoorman, Finalizer)
	return ctrl.Result{}, r.Patch(ctx, pgDoorman, patch)
}

// ensureFinalizer keeps the in-use finalizer in sync with the reference.
func (r *RenderedConfigReconciler) ensureFinalizer(
	ctx context.Context,
	pgDoorman *v1alpha1.PgDoorman,
	inUse bool,
) error {
	has := controllerutil.ContainsFinalizer(pgDoorman, Finalizer)
	if inUse == has {
		return nil
	}
	patch := client.MergeFrom(pgDoorman.DeepCopy())
	if inUse {
		controllerutil.AddFinalizer(pgDoorman, Finalizer)
	} else {
		controllerutil.RemoveFinalizer(pgDoorman, Finalizer)
	}
	return r.Patch(ctx, pgDoorman, patch)
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
	inPlaceUpgrades bool,
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

	binaryJSON, err := r.binarySpecJSON(inPlaceUpgrades)
	if err != nil {
		return err
	}
	if binaryJSON != nil {
		desired.Data[wrapper.BinarySpecKey] = binaryJSON
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
		string(existing.Data[generatedAdminPasswordKey]) == generatedAdmin &&
		string(existing.Data[wrapper.BinarySpecKey]) == string(binaryJSON) {
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

// binarySpecJSON marshals the desired binary spec, or returns nil when the
// cluster has not opted into in-place upgrades: the key then disappears from
// the Secret and wrappers keep the binary baked into their image.
func (r *RenderedConfigReconciler) binarySpecJSON(inPlaceUpgrades bool) ([]byte, error) {
	if r.Binary == nil || !inPlaceUpgrades {
		return nil, nil
	}
	return json.Marshal(r.Binary)
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
