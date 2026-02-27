package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	internalClient "github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/client"

	// Import test packages
	_ "github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/tests/pooler"
)

var _ = SynchronizedBeforeSuite(func(ctx SpecContext) []byte {
	cl, _, _, err := internalClient.NewClient()
	if err != nil {
		Fail(fmt.Sprintf("failed to create Kubernetes client: %v", err))
	}

	// Wait for plugin deployment to be ready
	deploy := types.NamespacedName{
		Namespace: "cnpg-system",
		Name:      "pg-doorman",
	}

	err = wait.PollUntilContextCancel(ctx, 5*time.Second, false,
		func(ctx context.Context) (bool, error) {
			var d appsv1.Deployment
			if err := cl.Get(ctx, deploy, &d); err != nil {
				return false, nil
			}
			return d.Status.ReadyReplicas > 0, nil
		})
	if err != nil {
		Fail(fmt.Sprintf("plugin deployment not ready: %v", err))
	}

	return []byte{}
}, func(_ []byte) {})

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pg-doorman e2e suite")
}
