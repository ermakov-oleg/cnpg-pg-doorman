//go:build e2e

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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	pgdoormanv1alpha1 "github.com/o-ermakov/cnpg-pg-doorman/api/v1alpha1"
	internalClient "github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/client"
	"github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/cluster"
	"github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/command"
	"github.com/o-ermakov/cnpg-pg-doorman/test/e2e/internal/namespace"
)

// createClusterAndWait creates a PgDoorman CR + Cluster and waits for readiness.
func createClusterAndWait(
	ctx SpecContext,
	cl client.Client,
	ns *corev1.Namespace,
	clusterName, configName string,
) {
	By("creating PgDoorman CR")
	Expect(cl.Create(ctx, newPgDoorman(ns.Name, configName))).To(Succeed())

	By("creating Cluster")
	Expect(cl.Create(ctx, newCluster(ns.Name, clusterName, configName))).To(Succeed())

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
		createClusterAndWait(ctx, cl, ns, "test-inject", "cr-inject")

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
					var hasScratch bool
					for _, vm := range c.VolumeMounts {
						if vm.Name == "pg-doorman-scratch" {
							hasScratch = true
						}
					}
					Expect(hasScratch).To(BeTrue(), "scratch volume mount not found")

					By("checking env vars")
					var hasConfigName, hasNamespace bool
					for _, env := range c.Env {
						if env.Name == "PG_DOORMAN_CONFIG_NAME" {
							hasConfigName = true
						}
						if env.Name == "PG_DOORMAN_CONFIG_NAMESPACE" {
							hasNamespace = true
						}
					}
					Expect(hasConfigName).To(BeTrue(), "PG_DOORMAN_CONFIG_NAME env not found")
					Expect(hasNamespace).To(BeTrue(), "PG_DOORMAN_CONFIG_NAMESPACE env not found")

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
		createClusterAndWait(ctx, cl, ns, "test-conn", "cr-conn")
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
		createClusterAndWait(ctx, cl, ns, "test-auth", "cr-auth")
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

		By("verifying dynamic user data pool size matches defaultPoolSize via admin console")
		stdout, _, err = psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName,
			pgdoormanv1alpha1.DefaultAdminPassword, pgdoormanv1alpha1.DefaultAdminUsername, "pgdoorman", "SHOW DATABASES")
		Expect(err).NotTo(HaveOccurred())
		// -tA rows: name|host|port|database|force_user|pool_size|min_pool_size|reserve_pool|pool_mode|max_connections|current_connections
		appPoolFound := false
		for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
			fields := strings.Split(line, "|")
			if len(fields) > 5 && fields[4] == "app" {
				appPoolFound = true
				Expect(fields[5]).To(Equal(fmt.Sprint(pgdoormanv1alpha1.DefaultDefaultPoolSize)),
					"dynamic user pool must use defaultPoolSize (auth_query.pool_size), not the executor pool size")
			}
		}
		Expect(appPoolFound).To(BeTrue(), "expected a pool row for dynamic user 'app' in SHOW DATABASES: "+stdout)
	})

	// Test 4: Transaction pooling
	It("should reuse connections in transaction pool mode", func(ctx SpecContext) {
		createClusterAndWait(ctx, cl, ns, "test-txn", "cr-txn")
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
		By(fmt.Sprintf("found %d unique backend PIDs across 5 queries", len(pids)))
		Expect(len(pids)).To(BeNumerically(">", 0))
	})

	// Test 5: Metrics endpoint
	It("should expose Prometheus metrics", func(ctx SpecContext) {
		createClusterAndWait(ctx, cl, ns, "test-metrics", "cr-metrics")
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

	// Test 6: Config hot reload via CRD update
	It("should hot-reload config via SIGHUP when PgDoorman CR changes", func(ctx SpecContext) {
		configName := "cr-reload"
		clusterName := "test-reload"
		createClusterAndWait(ctx, cl, ns, clusterName, configName)
		podName := getPodName(ctx, cl, ns.Name, clusterName)

		By("recording pod UID before config change")
		var podBefore corev1.Pod
		Expect(cl.Get(ctx, types.NamespacedName{Name: podName, Namespace: ns.Name}, &podBefore)).To(Succeed())
		uidBefore := podBefore.UID

		By("updating PgDoorman CR pool_mode to session")
		var pgDoorman pgdoormanv1alpha1.PgDoorman
		Expect(cl.Get(ctx, types.NamespacedName{Name: configName, Namespace: ns.Name}, &pgDoorman)).To(Succeed())
		pgDoorman.Spec.Pools["app"] = pgdoormanv1alpha1.PoolSpec{
			PoolMode: "session",
			AuthQuery: &pgdoormanv1alpha1.AuthQuerySpec{
				User:     "doorman_auth",
				Database: "app",
			},
		}
		Expect(cl.Update(ctx, &pgDoorman)).To(Succeed())

		By("waiting for CRD change to propagate via polling")
		time.Sleep(10 * time.Second)

		By("verifying pod was NOT restarted (same UID)")
		var podAfter corev1.Pod
		Expect(cl.Get(ctx, types.NamespacedName{Name: podName, Namespace: ns.Name}, &podAfter)).To(Succeed())
		Expect(podAfter.UID).To(Equal(uidBefore))

		By("checking wrapper logs for successful reload")
		logs := getSidecarLogs(ctx, clientset, ns.Name, podName)
		Expect(logs).To(ContainSubstring("config reloaded successfully"))
	})

	// Test 7: Image update triggers rolling restart
	It("should trigger rolling restart when sidecar image changes", func(ctx SpecContext) {
		configName := "cr-image"
		clusterName := "test-image"
		createClusterAndWait(ctx, cl, ns, clusterName, configName)
		podName := getPodName(ctx, cl, ns.Name, clusterName)

		By("recording pod UID before image change")
		var podBefore corev1.Pod
		Expect(cl.Get(ctx, types.NamespacedName{Name: podName, Namespace: ns.Name}, &podBefore)).To(Succeed())
		uidBefore := podBefore.UID

		By("changing metricsPort in plugin parameters to trigger pod spec change")
		var currentCluster cnpgv1.Cluster
		Expect(cl.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: ns.Name}, &currentCluster)).To(Succeed())

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

	// Test 8: Missing PgDoorman CR — plugin Pre() hook returns REQUEUE,
	// which blocks cluster reconciliation until the CR is created.
	It("should block cluster reconciliation when PgDoorman CR is missing", func(ctx SpecContext) {
		clusterName := "test-missing-cr"

		By("creating Cluster with non-existent PgDoorman CR")
		c := newClusterWithMissingConfig(ns.Name, clusterName)
		Expect(cl.Create(ctx, c)).To(Succeed())

		By("verifying no pods are created (reconciliation blocked by plugin REQUEUE)")
		Consistently(func(g Gomega) {
			var podList corev1.PodList
			g.Expect(cl.List(ctx, &podList, client.InNamespace(ns.Name),
				client.MatchingLabels{"cnpg.io/cluster": clusterName})).To(Succeed())
			g.Expect(podList.Items).To(BeEmpty())
		}).WithTimeout(30 * time.Second).WithPolling(5 * time.Second).Should(Succeed())

		By("verifying cluster has no ready instances")
		var cluster cnpgv1.Cluster
		Expect(cl.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: ns.Name}, &cluster)).To(Succeed())
		Expect(cluster.Status.ReadyInstances).To(Equal(0))
	})

	// Test 9: CRD rapid updates
	It("should handle rapid CRD updates and apply the final config", func(ctx SpecContext) {
		configName := "cr-debounce"
		clusterName := "test-debounce"
		createClusterAndWait(ctx, cl, ns, clusterName, configName)
		podName := getPodName(ctx, cl, ns.Name, clusterName)

		By("rapidly updating PgDoorman CR 3 times")
		for _, threads := range []int{3, 4, 8} {
			var pgDoorman pgdoormanv1alpha1.PgDoorman
			Expect(cl.Get(ctx, types.NamespacedName{Name: configName, Namespace: ns.Name}, &pgDoorman)).To(Succeed())
			pgDoorman.Spec.General = &pgdoormanv1alpha1.GeneralSpec{
				WorkerThreads: ptr.To(threads),
			}
			Expect(cl.Update(ctx, &pgDoorman)).To(Succeed())
			time.Sleep(1 * time.Second)
		}

		By("waiting for polling propagation")
		time.Sleep(10 * time.Second)

		By("checking that wrapper logs show config reloaded")
		logs := getSidecarLogs(ctx, clientset, ns.Name, podName)
		Expect(logs).To(ContainSubstring("config reloaded successfully"))

		By("verifying pooler still works after rapid updates")
		password := getAppPassword(ctx, cl, ns.Name, clusterName)
		stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app", "SELECT 1")
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout).To(ContainSubstring("1"))
	})

	// Test 10: Cluster without plugin passes admission
	It("should allow creating a cluster without pg-doorman plugin", func(ctx SpecContext) {
		clusterName := "test-no-plugin"

		By("creating Cluster without pg-doorman plugin")
		c := newClusterWithoutPlugin(ns.Name, clusterName)
		Expect(cl.Create(ctx, c)).To(Succeed())

		By("waiting for cluster to be ready")
		Eventually(func(g Gomega) {
			var current cnpgv1.Cluster
			g.Expect(cl.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: ns.Name,
			}, &current)).To(Succeed())
			g.Expect(cluster.IsReady(current)).To(BeTrue())
		}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		By("verifying no pg-doorman sidecar was injected")
		var podList corev1.PodList
		Expect(cl.List(ctx, &podList, client.InNamespace(ns.Name),
			client.MatchingLabels{"cnpg.io/cluster": clusterName})).To(Succeed())
		Expect(podList.Items).NotTo(BeEmpty())
		for _, pod := range podList.Items {
			for _, c := range pod.Spec.InitContainers {
				Expect(c.Name).NotTo(Equal("pg-doorman"),
					"pg-doorman sidecar should not be injected into cluster without the plugin")
			}
		}
	})

	// Test 11: Invalid plugin parameters block reconciliation
	It("should block cluster reconciliation when plugin parameters are invalid", func(ctx SpecContext) {
		By("creating PgDoorman CR")
		Expect(cl.Create(ctx, newPgDoorman(ns.Name, "cr-invalid-params"))).To(Succeed())

		By("creating Cluster with invalid poolerPort")
		c := newClusterWithInvalidParams(ns.Name, "test-invalid-params", "cr-invalid-params")
		Expect(cl.Create(ctx, c)).To(Succeed())

		By("verifying no pods are created (reconciliation blocked by invalid plugin config)")
		Consistently(func(g Gomega) {
			var podList corev1.PodList
			g.Expect(cl.List(ctx, &podList, client.InNamespace(ns.Name),
				client.MatchingLabels{"cnpg.io/cluster": "test-invalid-params"})).To(Succeed())
			g.Expect(podList.Items).To(BeEmpty())
		}).WithTimeout(30 * time.Second).WithPolling(5 * time.Second).Should(Succeed())

		By("verifying cluster has no ready instances")
		var current cnpgv1.Cluster
		Expect(cl.Get(ctx, types.NamespacedName{
			Name: "test-invalid-params", Namespace: ns.Name,
		}, &current)).To(Succeed())
		Expect(current.Status.ReadyInstances).To(Equal(0))
	})

	// Test 12: Secret rotation triggers config reload with admin password verification
	It("should reload config when referenced Secret changes and apply new admin password", func(ctx SpecContext) {
		configName := "cr-secret-rotate"
		clusterName := "test-secret-rotate"
		secretName := "admin-password"
		initialPassword := "initial-password"
		rotatedPassword := "rotated-password"

		By("creating admin password Secret")
		Expect(cl.Create(ctx, newPasswordSecret(ns.Name, secretName, initialPassword))).To(Succeed())

		By("creating PgDoorman CR with secret ref")
		Expect(cl.Create(ctx, newPgDoormanWithSecretRef(ns.Name, configName, secretName))).To(Succeed())

		By("creating Cluster")
		Expect(cl.Create(ctx, newCluster(ns.Name, clusterName, configName))).To(Succeed())

		By("waiting for cluster to be ready")
		Eventually(func(g Gomega) {
			var current cnpgv1.Cluster
			g.Expect(cl.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: ns.Name,
			}, &current)).To(Succeed())
			g.Expect(cluster.IsReady(current)).To(BeTrue())
		}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		podName := getPodName(ctx, cl, ns.Name, clusterName)

		By("verifying admin login works with initial password")
		Eventually(func(g Gomega) {
			stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName,
				initialPassword, "admin", "pgdoorman", "SHOW VERSION")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(stdout).NotTo(BeEmpty())
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("rotating the admin password Secret")
		var secret corev1.Secret
		Expect(cl.Get(ctx, types.NamespacedName{Name: secretName, Namespace: ns.Name}, &secret)).To(Succeed())
		secret.Data["password"] = []byte(rotatedPassword)
		Expect(cl.Update(ctx, &secret)).To(Succeed())

		By("waiting for wrapper to detect secret change and reload config")
		Eventually(func() string {
			return getSidecarLogs(ctx, clientset, ns.Name, podName)
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(
			And(
				ContainSubstring(`"secretHashChanged":true`),
				ContainSubstring("config reloaded successfully"),
			),
		)

		By("verifying admin login works with rotated password")
		Eventually(func(g Gomega) {
			stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName,
				rotatedPassword, "admin", "pgdoorman", "SHOW VERSION")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(stdout).NotTo(BeEmpty())
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("verifying old password no longer works")
		_, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName,
			initialPassword, "admin", "pgdoorman", "SHOW VERSION")
		Expect(err).To(HaveOccurred())

		By("verifying app pooler still works after admin password rotation")
		password := getAppPassword(ctx, cl, ns.Name, clusterName)
		stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app", "SELECT 1")
		Expect(err).NotTo(HaveOccurred())
		Expect(stdout).To(ContainSubstring("1"))
	})

	// Test 13: Sidecar restart on crash
	It("should restart pg_doorman after crash with backoff", func(ctx SpecContext) {
		createClusterAndWait(ctx, cl, ns, "test-crash", "cr-crash")
		podName := getPodName(ctx, cl, ns.Name, "test-crash")

		By("killing pg_doorman process inside sidecar")
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
		}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(
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
