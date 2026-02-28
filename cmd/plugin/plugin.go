package plugin

import (
	"github.com/cloudnative-pg/cnpg-i/pkg/lifecycle"
	"github.com/cloudnative-pg/cnpg-i/pkg/operator"
	"github.com/cloudnative-pg/cnpg-i-machinery/pkg/pluginhelper/http"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	pluginIdentity "github.com/o-ermakov/cnpg-pg-doorman/internal/identity"
	pluginLifecycle "github.com/o-ermakov/cnpg-pg-doorman/internal/lifecycle"
	pluginOperator "github.com/o-ermakov/cnpg-pg-doorman/internal/operator"
)

func NewCmd() *cobra.Command {
	enricher := func(server *grpc.Server) error {
		lifecycle.RegisterOperatorLifecycleServer(server, pluginLifecycle.Implementation{})
		operator.RegisterOperatorServer(server, pluginOperator.Implementation{})
		return nil
	}

	return http.CreateMainCmd(pluginIdentity.Implementation{}, enricher)
}
