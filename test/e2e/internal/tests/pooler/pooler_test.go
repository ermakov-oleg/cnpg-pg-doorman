package pooler

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	internalClient "github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/client"
	"github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/cluster"
	"github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/command"
	"github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/namespace"
)

var _ = Describe("pg_doorman pooler", func() {
	var ns *corev1.Namespace
	var cl client.Client

	BeforeEach(func(ctx SpecContext) {
		var err error
		cl, _, _, err = internalClient.NewClient()
		Expect(err).NotTo(HaveOccurred())
		ns, err = namespace.CreateUniqueNamespace(ctx, cl, "pooler")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func(ctx SpecContext) {
		if ns != nil {
			_ = cl.Delete(ctx, ns)
		}
	})

	It("should inject pg_doorman sidecar into pods", func(ctx SpecContext) {
		configMapName := "test-pg-doorman"
		clusterName := "test-cluster"

		By("creating ConfigMap")
		cm := newConfigMap(ns.Name, configMapName)
		Expect(cl.Create(ctx, cm)).To(Succeed())

		By("creating Cluster")
		c := newCluster(ns.Name, clusterName, configMapName)
		Expect(cl.Create(ctx, c)).To(Succeed())

		By("waiting for cluster to be ready")
		Eventually(func(g Gomega) {
			var current cnpgv1.Cluster
			g.Expect(cl.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: ns.Name,
			}, &current)).To(Succeed())
			g.Expect(cluster.IsReady(current)).To(BeTrue())
		}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		By("checking pg_doorman sidecar exists in pod")
		var podList corev1.PodList
		Expect(cl.List(ctx, &podList, client.InNamespace(ns.Name))).To(Succeed())
		Expect(podList.Items).NotTo(BeEmpty())

		found := false
		for _, pod := range podList.Items {
			for _, c := range pod.Spec.InitContainers {
				if c.Name == "pg-doorman" {
					found = true
					Expect(c.RestartPolicy).NotTo(BeNil())
					Expect(*c.RestartPolicy).To(Equal(corev1.ContainerRestartPolicyAlways))
				}
			}
		}
		Expect(found).To(BeTrue(), "pg-doorman sidecar not found in any pod")
	})

	It("should accept connections via pooler port", func(ctx SpecContext) {
		configMapName := "test-pg-doorman-conn"
		clusterName := "test-cluster-conn"

		By("creating ConfigMap and Cluster")
		Expect(cl.Create(ctx, newConfigMap(ns.Name, configMapName))).To(Succeed())
		c := newCluster(ns.Name, clusterName, configMapName)
		Expect(cl.Create(ctx, c)).To(Succeed())

		By("waiting for cluster to be ready")
		Eventually(func(g Gomega) {
			var current cnpgv1.Cluster
			g.Expect(cl.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: ns.Name,
			}, &current)).To(Succeed())
			g.Expect(cluster.IsReady(current)).To(BeTrue())
		}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		By("executing SELECT 1 via pooler port")
		_, clientset, restConfig, err := internalClient.NewClient()
		Expect(err).NotTo(HaveOccurred())

		podName := fmt.Sprintf("%s-1", clusterName)
		stdout, _, err := command.ExecuteInContainer(ctx, clientset, restConfig,
			command.ContainerLocator{
				NamespaceName: ns.Name,
				PodName:       podName,
				ContainerName: "postgres",
			},
			nil,
			[]string{"psql", "-h", "localhost", "-p", "6432", "-U", "app", "-d", "app", "-tAc", "SELECT 1"},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout).To(ContainSubstring("1"))
	})
})
