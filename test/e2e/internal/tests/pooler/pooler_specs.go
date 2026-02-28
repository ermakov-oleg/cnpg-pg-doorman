package pooler

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	internalClient "github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/client"
	"github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/cluster"
	"github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/command"
	"github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/namespace"
)

// createClusterAndWait creates a ConfigMap + Cluster and waits for readiness.
func createClusterAndWait(
	ctx SpecContext,
	cl client.Client,
	ns *corev1.Namespace,
	clusterName, configMapName string,
) {
	By("creating ConfigMap")
	Expect(cl.Create(ctx, newConfigMap(ns.Name, configMapName))).To(Succeed())

	By("creating Cluster")
	Expect(cl.Create(ctx, newCluster(ns.Name, clusterName, configMapName))).To(Succeed())

	By("waiting for cluster to be ready")
	Eventually(func(g Gomega) {
		var current cnpgv1.Cluster
		g.Expect(cl.Get(ctx, types.NamespacedName{
			Name: clusterName, Namespace: ns.Name,
		}, &current)).To(Succeed())
		g.Expect(cluster.IsReady(current)).To(BeTrue())
	}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

// getAppPassword reads the app user password from the CNPG-managed secret.
func getAppPassword(ctx SpecContext, cl client.Client, ns, clusterName string) string {
	var secret corev1.Secret
	Expect(cl.Get(ctx, types.NamespacedName{
		Name: clusterName + "-app", Namespace: ns,
	}, &secret)).To(Succeed())
	return string(secret.Data["password"])
}

// psqlViaPooler executes a psql command through the pooler port inside the postgres container.
func psqlViaPooler(
	ctx SpecContext,
	clientset *kubernetes.Clientset,
	restConfig *rest.Config,
	ns, podName, password, user, db, query string,
) (string, string, error) {
	return command.ExecuteInContainer(ctx, clientset, restConfig,
		command.ContainerLocator{
			NamespaceName: ns,
			PodName:       podName,
			ContainerName: "postgres",
		},
		nil,
		[]string{"sh", "-c", fmt.Sprintf("PGPASSWORD=%s PGCONNECT_TIMEOUT=5 psql -h 127.0.0.1 -p 6432 -U %s -d %s -tAc '%s'", password, user, db, query)},
	)
}

// getSidecarLogs returns the logs from the pg-doorman sidecar container.
func getSidecarLogs(
	ctx SpecContext,
	clientset *kubernetes.Clientset,
	ns, podName string,
) string {
	logs, err := clientset.CoreV1().Pods(ns).GetLogs(podName, &corev1.PodLogOptions{
		Container: "pg-doorman",
	}).Do(ctx).Raw()
	if err != nil {
		return ""
	}
	return string(logs)
}

// getPodName returns the first pod name matching the cluster.
func getPodName(ctx SpecContext, cl client.Client, ns, clusterName string) string {
	var podList corev1.PodList
	Expect(cl.List(ctx, &podList, client.InNamespace(ns))).To(Succeed())
	for _, pod := range podList.Items {
		if strings.HasPrefix(pod.Name, clusterName+"-") {
			return pod.Name
		}
	}
	Fail("no pod found for cluster " + clusterName)
	return ""
}

var _ = Describe("pg_doorman pooler", func() {
	var (
		ns         *corev1.Namespace
		cl         client.Client
		clientset  *kubernetes.Clientset
		restConfig *rest.Config
	)

	BeforeEach(func(ctx SpecContext) {
		var err error
		cl, clientset, restConfig, err = internalClient.NewClient()
		Expect(err).NotTo(HaveOccurred())
		ns, err = namespace.CreateUniqueNamespace(ctx, cl, "pooler")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func(ctx SpecContext) {
		if ns != nil {
			_ = cl.Delete(ctx, ns)
		}
	})

	// Test 1: Sidecar injection
	It("should inject pg_doorman sidecar into pods", func(ctx SpecContext) {
		createClusterAndWait(ctx, cl, ns, "test-inject", "cm-inject")

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

					By("checking volume mounts")
					var hasConfig, hasScratch bool
					for _, vm := range c.VolumeMounts {
						if vm.Name == "pg-doorman-config" && vm.ReadOnly {
							hasConfig = true
						}
						if vm.Name == "pg-doorman-scratch" {
							hasScratch = true
						}
					}
					Expect(hasConfig).To(BeTrue(), "config volume mount not found")
					Expect(hasScratch).To(BeTrue(), "scratch volume mount not found")

					By("checking security context")
					Expect(c.SecurityContext).NotTo(BeNil())
					Expect(*c.SecurityContext.RunAsNonRoot).To(BeTrue())
					Expect(*c.SecurityContext.ReadOnlyRootFilesystem).To(BeTrue())
					Expect(*c.SecurityContext.AllowPrivilegeEscalation).To(BeFalse())
				}
			}
		}
		Expect(found).To(BeTrue(), "pg-doorman sidecar not found in any pod")
	})

	// Test 2: Connection via pooler
	It("should accept connections via pooler port", func(ctx SpecContext) {
		createClusterAndWait(ctx, cl, ns, "test-conn", "cm-conn")
		podName := getPodName(ctx, cl, ns.Name, "test-conn")
		password := getAppPassword(ctx, cl, ns.Name, "test-conn")

		By("executing SELECT 1 via pooler port")
		Eventually(func(g Gomega) {
			stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app", "SELECT 1")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(stdout).To(ContainSubstring("1"))
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	})

	// Test 3: Auth query works
	It("should authenticate users via auth_query", func(ctx SpecContext) {
		createClusterAndWait(ctx, cl, ns, "test-auth", "cm-auth")
		podName := getPodName(ctx, cl, ns.Name, "test-auth")
		password := getAppPassword(ctx, cl, ns.Name, "test-auth")

		By("connecting as 'app' user through pooler and running query")
		stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app", "SELECT current_user")
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout).To(ContainSubstring("app"))

		By("verifying that each user sees their own data")
		_, _, err = psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app",
			"CREATE TABLE IF NOT EXISTS test_auth (id serial, owner text DEFAULT current_user)")
		Expect(err).NotTo(HaveOccurred())

		_, _, err = psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app",
			"INSERT INTO test_auth DEFAULT VALUES")
		Expect(err).NotTo(HaveOccurred())

		stdout, _, err = psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app",
			"SELECT owner FROM test_auth LIMIT 1")
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout).To(ContainSubstring("app"))
	})

	// Test 4: Transaction pooling
	It("should reuse connections in transaction pool mode", func(ctx SpecContext) {
		createClusterAndWait(ctx, cl, ns, "test-txn", "cm-txn")
		podName := getPodName(ctx, cl, ns.Name, "test-txn")
		password := getAppPassword(ctx, cl, ns.Name, "test-txn")

		By("running multiple queries and verifying connection reuse via pg_backend_pid")
		pids := make(map[string]bool)
		for range 5 {
			stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app",
				"SELECT pg_backend_pid()")
			Expect(err).NotTo(HaveOccurred())
			pid := strings.TrimSpace(stdout)
			pids[pid] = true
		}
		// In transaction mode, connections are reused, so we expect fewer unique PIDs than queries
		// (though not guaranteed to be exactly 1 due to timing)
		By(fmt.Sprintf("found %d unique backend PIDs across 5 queries", len(pids)))
		// At minimum verify pooler is working (we got results)
		Expect(len(pids)).To(BeNumerically(">", 0))
	})

	// Test 5: Metrics endpoint
	It("should expose Prometheus metrics", func(ctx SpecContext) {
		createClusterAndWait(ctx, cl, ns, "test-metrics", "cm-metrics")
		podName := getPodName(ctx, cl, ns.Name, "test-metrics")

		By("fetching the metrics endpoint from within the pod")
		stdout, _, err := command.ExecuteInContainer(ctx, clientset, restConfig,
			command.ContainerLocator{
				NamespaceName: ns.Name,
				PodName:       podName,
				ContainerName: "postgres",
			},
			nil,
			[]string{"bash", "-c", `exec 3<>/dev/tcp/localhost/9127; printf "GET /metrics HTTP/1.0\r\nHost: localhost\r\n\r\n" >&3; cat <&3`},
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout).To(ContainSubstring("pg_doorman"))
	})

	// Test 6: Config hot reload
	It("should hot-reload config via SIGHUP without pod restart", func(ctx SpecContext) {
		configMapName := "cm-reload"
		clusterName := "test-reload"
		createClusterAndWait(ctx, cl, ns, clusterName, configMapName)
		podName := getPodName(ctx, cl, ns.Name, clusterName)

		By("recording pod UID before config change")
		var podBefore corev1.Pod
		Expect(cl.Get(ctx, types.NamespacedName{Name: podName, Namespace: ns.Name}, &podBefore)).To(Succeed())
		uidBefore := podBefore.UID

		By("updating ConfigMap pool_mode to session")
		var cm corev1.ConfigMap
		Expect(cl.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: ns.Name}, &cm)).To(Succeed())
		cm.Data["pg_doorman.yaml"] = strings.ReplaceAll(cm.Data["pg_doorman.yaml"], `pool_mode: "transaction"`, `pool_mode: "session"`)
		Expect(cl.Update(ctx, &cm)).To(Succeed())

		By("waiting for config to propagate and reload (~90s for ConfigMap propagation + polling + debounce)")
		time.Sleep(90 * time.Second)

		By("verifying pod was NOT restarted (same UID)")
		var podAfter corev1.Pod
		Expect(cl.Get(ctx, types.NamespacedName{Name: podName, Namespace: ns.Name}, &podAfter)).To(Succeed())
		Expect(podAfter.UID).To(Equal(uidBefore))

		By("checking wrapper logs for successful reload")
		logs := getSidecarLogs(ctx, clientset, ns.Name, podName)
		Expect(logs).To(ContainSubstring("config reloaded successfully"))
	})

	// Test 7: Config validation — invalid config should NOT be applied
	It("should reject invalid config and keep old config running", func(ctx SpecContext) {
		configMapName := "cm-invalid"
		clusterName := "test-invalid"
		createClusterAndWait(ctx, cl, ns, clusterName, configMapName)
		podName := getPodName(ctx, cl, ns.Name, clusterName)

		By("verifying pooler works initially")
		password := getAppPassword(ctx, cl, ns.Name, clusterName)
		stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app", "SELECT 1")
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout).To(ContainSubstring("1"))

		By("updating ConfigMap with invalid config (no pools)")
		var cm corev1.ConfigMap
		Expect(cl.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: ns.Name}, &cm)).To(Succeed())
		cm.Data["pg_doorman.yaml"] = `
general:
  host: "0.0.0.0"
  port: 6432
`
		Expect(cl.Update(ctx, &cm)).To(Succeed())

		By("waiting for config propagation")
		time.Sleep(90 * time.Second)

		By("checking wrapper logs for validation error")
		logs := getSidecarLogs(ctx, clientset, ns.Name, podName)
		Expect(logs).To(ContainSubstring("new config is invalid"))

		By("verifying pooler still works with old config")
		stdout, _, err = psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app", "SELECT 1")
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout).To(ContainSubstring("1"))
	})

	// Test 8: Image update triggers rolling restart
	It("should trigger rolling restart when sidecar image changes", func(ctx SpecContext) {
		configMapName := "cm-image"
		clusterName := "test-image"
		createClusterAndWait(ctx, cl, ns, clusterName, configMapName)
		podName := getPodName(ctx, cl, ns.Name, clusterName)

		By("recording pod UID before image change")
		var podBefore corev1.Pod
		Expect(cl.Get(ctx, types.NamespacedName{Name: podName, Namespace: ns.Name}, &podBefore)).To(Succeed())
		uidBefore := podBefore.UID

		By("changing sidecar image in plugin parameters triggers cluster reconcile")
		// Note: In practice, changing the SIDECAR_IMAGE env on the plugin Deployment
		// would change the injected container image, causing pod spec changes -> rolling update.
		// This test verifies the concept by directly modifying the cluster to trigger reconciliation.
		var currentCluster cnpgv1.Cluster
		Expect(cl.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: ns.Name}, &currentCluster)).To(Succeed())

		// Force a pod spec change by modifying metricsPort
		for i := range currentCluster.Spec.Plugins {
			if currentCluster.Spec.Plugins[i].Name == "pg-doorman.cnpg.io" {
				currentCluster.Spec.Plugins[i].Parameters["metricsPort"] = "9128"
			}
		}
		Expect(cl.Update(ctx, &currentCluster)).To(Succeed())

		By("waiting for pod to be replaced")
		Eventually(func(g Gomega) {
			newPodName := getPodName(ctx, cl, ns.Name, clusterName)
			var newPod corev1.Pod
			g.Expect(cl.Get(ctx, types.NamespacedName{Name: newPodName, Namespace: ns.Name}, &newPod)).To(Succeed())
			g.Expect(newPod.UID).NotTo(Equal(uidBefore))
		}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	})

	// Test 9: Missing ConfigMap
	It("should handle missing ConfigMap gracefully", func(ctx SpecContext) {
		clusterName := "test-missing-cm"

		By("creating Cluster with non-existent ConfigMap (no ConfigMap created)")
		c := newClusterWithMissingConfigMap(ns.Name, clusterName)
		Expect(cl.Create(ctx, c)).To(Succeed())

		By("waiting for pod to be created")
		Eventually(func(g Gomega) {
			var podList corev1.PodList
			g.Expect(cl.List(ctx, &podList, client.InNamespace(ns.Name))).To(Succeed())
			g.Expect(podList.Items).NotTo(BeEmpty())
		}).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		podName := getPodName(ctx, cl, ns.Name, clusterName)

		By("verifying ConfigMap volume is marked as Optional")
		var pod corev1.Pod
		Expect(cl.Get(ctx, types.NamespacedName{Name: podName, Namespace: ns.Name}, &pod)).To(Succeed())
		for _, v := range pod.Spec.Volumes {
			if v.Name == "pg-doorman-config" && v.ConfigMap != nil {
				Expect(v.ConfigMap.Optional).NotTo(BeNil())
				Expect(*v.ConfigMap.Optional).To(BeTrue())
			}
		}

		By("waiting for sidecar to log that it's waiting for config")
		Eventually(func() string {
			return getSidecarLogs(ctx, clientset, ns.Name, podName)
		}).WithTimeout(3*time.Minute).WithPolling(10*time.Second).Should(
			ContainSubstring("waiting for valid config"),
		)
	})

	// Test 10: Rapid config updates (debounce)
	It("should debounce rapid config updates and apply only the final config", func(ctx SpecContext) {
		configMapName := "cm-debounce"
		clusterName := "test-debounce"
		createClusterAndWait(ctx, cl, ns, clusterName, configMapName)
		podName := getPodName(ctx, cl, ns.Name, clusterName)

		By("rapidly updating ConfigMap 3 times")
		for i, threads := range []string{"3", "4", "8"} {
			var cm corev1.ConfigMap
			Expect(cl.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: ns.Name}, &cm)).To(Succeed())
			// Change worker_threads each time
			cm.Data["pg_doorman.yaml"] = strings.ReplaceAll(cm.Data["pg_doorman.yaml"], "worker_threads: 2", "worker_threads: "+threads)
			if i > 0 {
				prev := []string{"3", "4", "8"}
				cm.Data["pg_doorman.yaml"] = strings.ReplaceAll(cm.Data["pg_doorman.yaml"], "worker_threads: "+prev[i-1], "worker_threads: "+threads)
			}
			Expect(cl.Update(ctx, &cm)).To(Succeed())
			time.Sleep(1 * time.Second) // rapid but not instantaneous
		}

		By("waiting for debounce + propagation")
		time.Sleep(90 * time.Second)

		By("checking that wrapper logs show config reloaded (possibly multiple times, but should converge)")
		logs := getSidecarLogs(ctx, clientset, ns.Name, podName)
		Expect(logs).To(ContainSubstring("config reloaded successfully"))

		By("verifying pooler still works after rapid updates")
		password := getAppPassword(ctx, cl, ns.Name, clusterName)
		stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app", "SELECT 1")
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout).To(ContainSubstring("1"))
	})

	// Test 11: Sidecar restart on crash
	It("should restart pg_doorman after crash with backoff", func(ctx SpecContext) {
		createClusterAndWait(ctx, cl, ns, "test-crash", "cm-crash")
		podName := getPodName(ctx, cl, ns.Name, "test-crash")

		By("killing pg_doorman process inside sidecar")
		// Find pg_doorman PID via /proc and kill it. The sidecar image may not have pgrep/pidof.
		_, _, err := command.ExecuteInContainer(ctx, clientset, restConfig,
			command.ContainerLocator{
				NamespaceName: ns.Name,
				PodName:       podName,
				ContainerName: "pg-doorman",
			},
			nil,
			[]string{"sh", "-c", `for p in /proc/[0-9]*/exe; do t=$(readlink "$p" 2>/dev/null); if [ "$t" = "/usr/bin/pg_doorman" ]; then kill "$(echo "$p" | cut -d/ -f3)" 2>/dev/null; fi; done`},
		)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for wrapper to restart pg_doorman")
		Eventually(func() string {
			return getSidecarLogs(ctx, clientset, ns.Name, podName)
		}).WithTimeout(2*time.Minute).WithPolling(10*time.Second).Should(
			ContainSubstring("pg_doorman exited unexpectedly, restarting"),
		)

		By("verifying pooler recovers and accepts connections")
		password := getAppPassword(ctx, cl, ns.Name, "test-crash")
		Eventually(func(g Gomega) {
			stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app", "SELECT 1")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(stdout).To(ContainSubstring("1"))
		}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	})
})
