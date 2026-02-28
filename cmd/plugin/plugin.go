package plugin

import (
	"context"
	"log/slog"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/http"
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/cnpg-i/pkg/reconciler"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
	pluginIdentity "github.com/o-ermakov/cnpg-pg-doorman/internal/identity"
	pluginLifecycle "github.com/o-ermakov/cnpg-pg-doorman/internal/lifecycle"
	pluginOperator "github.com/o-ermakov/cnpg-pg-doorman/internal/operator"
	pluginReconciler "github.com/o-ermakov/cnpg-pg-doorman/internal/reconciler"
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

// Start starts the gRPC server.
func (c *CNPGI) Start(ctx context.Context) error {
	enrich := func(server *grpc.Server) error {
		reconciler.RegisterReconcilerHooksServer(server, pluginReconciler.Implementation{
			Client: c.Client,
		})
		lifecycle.RegisterOperatorLifecycleServer(server, pluginLifecycle.Implementation{})
		operator.RegisterOperatorServer(server, pluginOperator.Implementation{})
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

// NewCmd creates the serve command with ctrl.NewManager for k8s client access.
func NewCmd() *cobra.Command {
	// Use CreateMainCmd to get the command with all standard flags (TLS, etc.)
	cmd := http.CreateMainCmd(pluginIdentity.Implementation{})

	// Override RunE to create a manager first (needed for k8s client in ReconcilerHooks)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx := cmd.Context()

		mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
			Scheme: scheme,
			Metrics: metricsserver.Options{
				BindAddress: "0", // disabled
			},
			Client: client.Options{
				Cache: &client.CacheOptions{
					// Disable cache for RBAC types — direct API reads avoid
					// needing cluster-wide list/watch on roles and rolebindings.
					DisableFor: []client.Object{
						&rbacv1.Role{},
						&rbacv1.RoleBinding{},
					},
				},
			},
		})
		if err != nil {
			slog.Error("unable to start manager", "error", err)
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
			slog.Error("unable to add CNPGI runnable", "error", err)
			return err
		}

		slog.Info("starting manager")
		return mgr.Start(ctx)
	}

	return cmd
}
