package plugin

import (
	"context"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/http"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/cnpg-i/pkg/reconciler"
	"github.com/cloudnative-pg/machinery/pkg/log"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/ermakov-oleg/cnpg-pg-doorman/api/v1alpha1"
	"github.com/ermakov-oleg/cnpg-pg-doorman/internal/controller"
	pluginIdentity "github.com/ermakov-oleg/cnpg-pg-doorman/internal/identity"
	pluginLifecycle "github.com/ermakov-oleg/cnpg-pg-doorman/internal/lifecycle"
	pluginOperator "github.com/ermakov-oleg/cnpg-pg-doorman/internal/operator"
	pluginReconciler "github.com/ermakov-oleg/cnpg-pg-doorman/internal/reconciler"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(cnpgv1.AddToScheme(scheme))
}

// CNPGI is the CNPG-i gRPC server as a manager runnable.
type CNPGI struct {
	Client         client.Client
	PluginPath     string
	ServerCertPath string
	ServerKeyPath  string
	ClientCertPath string
	ServerAddress  string
}

// NeedLeaderElection keeps the gRPC server active on every replica: CNPG
// hooks must be served by all pods behind the Service. Leader election only
// gates controller runnables.
func (c *CNPGI) NeedLeaderElection() bool {
	return false
}

// Start starts the gRPC server.
func (c *CNPGI) Start(ctx context.Context) error {
	enrich := func(server *grpc.Server) error {
		reconciler.RegisterReconcilerHooksServer(server, pluginReconciler.Implementation{
			Client: c.Client,
		})
		lifecycle.RegisterOperatorLifecycleServer(server, pluginLifecycle.Implementation{})
		operator.RegisterOperatorServer(server, pluginOperator.Implementation{Client: c.Client})
		return nil
	}

	srv := http.Server{
		IdentityImpl:   pluginIdentity.Implementation{},
		Enrichers:      []http.ServerEnricher{enrich},
		PluginPath:     c.PluginPath,
		ServerCertPath: c.ServerCertPath,
		ServerKeyPath:  c.ServerKeyPath,
		ClientCertPath: c.ClientCertPath,
		ServerAddress:  c.ServerAddress,
	}

	return srv.Start(ctx)
}

// secretLabelSelector narrows the Secret informer to cluster-owned or
// explicitly allowed secrets.
func secretLabelSelector() labels.Selector {
	owned, _ := labels.NewRequirement(controller.ClusterLabel, selection.Exists, nil)
	return labels.NewSelector().Add(*owned)
}

func slogError(ctx context.Context, err error, msg string) {
	log.FromContext(ctx).Error(err, msg)
}

// NewCmd creates the serve command with ctrl.NewManager for k8s client access.
func NewCmd() *cobra.Command {
	// Use CreateMainCmd to get the command with all standard flags (TLS, etc.)
	cmd := http.CreateMainCmd(pluginIdentity.Implementation{})

	cmd.Flags().Bool("leader-elect", false,
		"Enable leader election: controller runnables run on one replica only, gRPC hooks are served by all replicas")
	_ = viper.BindPFlag("leader-elect", cmd.Flags().Lookup("leader-elect"))
	cmd.Flags().String("health-probe-bind-address", ":8081", "The address the probe endpoint binds to")
	_ = viper.BindPFlag("health-probe-bind-address", cmd.Flags().Lookup("health-probe-bind-address"))
	cmd.Flags().String("metrics-bind-address", ":8080", "The address the metrics endpoint binds to ('0' disables it)")
	_ = viper.BindPFlag("metrics-bind-address", cmd.Flags().Lookup("metrics-bind-address"))

	// Override RunE to create a manager first (needed for k8s client in ReconcilerHooks)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()
		logger := log.FromContext(ctx)

		mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
			Scheme: scheme,
			Metrics: metricsserver.Options{
				BindAddress: viper.GetString("metrics-bind-address"),
			},
			HealthProbeBindAddress: viper.GetString("health-probe-bind-address"),
			LeaderElection:         viper.GetBool("leader-elect"),
			LeaderElectionID:       "pg-doorman.cnpg.io",
			// Step down voluntarily on shutdown so a standby replica can take
			// over without waiting for the lease to expire.
			LeaderElectionReleaseOnCancel: true,
			Cache: cache.Options{
				ByObject: map[client.Object]cache.ByObject{
					// The rendering controller watches only cluster-scoped
					// secrets (CNPG labels its own; users label custom ones):
					// a full cluster-wide Secret informer would cache every
					// secret in the cluster.
					&corev1.Secret{}: {
						Label: secretLabelSelector(),
					},
				},
			},
			Client: client.Options{
				Cache: &client.CacheOptions{
					// Disable cache for types read rarely and without
					// cluster-wide list/watch RBAC.
					DisableFor: []client.Object{
						&rbacv1.Role{},
						&rbacv1.RoleBinding{},
						&corev1.Service{},
					},
				},
			},
		})
		if err != nil {
			logger.Error(err, "unable to start manager")
			return err
		}

		if err := (&controller.RenderedConfigReconciler{
			Client:   mgr.GetClient(),
			Recorder: mgr.GetEventRecorderFor("pg-doorman-render"),
		}).SetupWithManager(mgr); err != nil {
			slogError(ctx, err, "unable to set up rendered config controller")
			return err
		}

		if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
			logger.Error(err, "unable to set up health check")
			return err
		}
		if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
			logger.Error(err, "unable to set up ready check")
			return err
		}

		if err := mgr.Add(&CNPGI{
			Client:         mgr.GetClient(),
			PluginPath:     viper.GetString("plugin-path"),
			ServerCertPath: viper.GetString("server-cert"),
			ServerKeyPath:  viper.GetString("server-key"),
			ClientCertPath: viper.GetString("client-cert"),
			ServerAddress:  viper.GetString("server-address"),
		}); err != nil {
			logger.Error(err, "unable to add CNPGI runnable")
			return err
		}

		logger.Info("starting manager")
		return mgr.Start(ctx)
	}

	return cmd
}
