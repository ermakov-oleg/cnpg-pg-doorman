//go:build e2e

package pooler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	cnpgv1 "github.com/cloudnative-pg/api/pkg/api/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	internalClient "github.com/ermakov-oleg/cnpg-pg-doorman/test/e2e/internal/client"
	"github.com/ermakov-oleg/cnpg-pg-doorman/test/e2e/internal/cluster"
	"github.com/ermakov-oleg/cnpg-pg-doorman/test/e2e/internal/command"
	"github.com/ermakov-oleg/cnpg-pg-doorman/test/e2e/internal/namespace"
)

const (
	pluginNamespace  = "cnpg-system"
	pluginDeployment = "pg-doorman"
	// pluginImageCurrent and pluginImageNext serve the same plugin code but a
	// content-distinct pg_doorman binary (see build-plugin-next-image), so
	// switching between them re-renders binary.json without any pod spec change.
	pluginImageCurrent = "pg-doorman-plugin:testing"
	pluginImageNext    = "pg-doorman-plugin:testing-next"
	// wrapperHealthPort serves the wrapper's own /metrics next to /healthz.
	wrapperHealthPort = 8081
)

// setPluginImage points the plugin Deployment at image and waits for the
// rollout to finish.
func setPluginImage(ctx SpecContext, cl client.Client, image string) {
	key := types.NamespacedName{Name: pluginDeployment, Namespace: pluginNamespace}

	By("setting plugin Deployment image to " + image)
	Eventually(func(g Gomega) {
		var deploy appsv1.Deployment
		g.Expect(cl.Get(ctx, key, &deploy)).To(Succeed())
		deploy.Spec.Template.Spec.Containers[0].Image = image
		g.Expect(cl.Update(ctx, &deploy)).To(Succeed())
	}).WithTimeout(1 * time.Minute).WithPolling(2 * time.Second).Should(Succeed())

	By("waiting for the plugin rollout to complete")
	Eventually(func(g Gomega) {
		var deploy appsv1.Deployment
		g.Expect(cl.Get(ctx, key, &deploy)).To(Succeed())
		g.Expect(deploy.Spec.Template.Spec.Containers[0].Image).To(Equal(image))
		g.Expect(deploy.Status.ObservedGeneration).To(BeNumerically(">=", deploy.Generation))
		var replicas int32 = 1
		if deploy.Spec.Replicas != nil {
			replicas = *deploy.Spec.Replicas
		}
		// Same conditions as `kubectl rollout status`: updatedReplicas alone
		// counts pods that are not ready yet, and total replicas must be back
		// to spec so no old-image pod is still serving.
		g.Expect(deploy.Status.UpdatedReplicas).To(Equal(replicas))
		g.Expect(deploy.Status.Replicas).To(Equal(replicas))
		g.Expect(deploy.Status.AvailableReplicas).To(Equal(replicas))
	}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
}

// runtimeBinarySHA returns the sha256 of the pg_doorman the wrapper actually runs.
func runtimeBinarySHA(
	ctx SpecContext,
	clientset *kubernetes.Clientset,
	restConfig *rest.Config,
	ns, podName string,
) string {
	stdout, stderr, err := command.ExecuteInContainer(ctx, clientset, restConfig,
		command.ContainerLocator{
			NamespaceName: ns,
			PodName:       podName,
			ContainerName: "pg-doorman",
		},
		nil,
		[]string{"sh", "-c", "sha256sum /tmp/bin/pg_doorman | cut -d' ' -f1"},
	)
	Expect(err).NotTo(HaveOccurred(), "sha256sum failed: "+stderr)
	sha := strings.TrimSpace(stdout)
	Expect(sha).NotTo(BeEmpty())
	return sha
}

// getWrapperLogs keeps only the wrapper's own JSON lines: the sidecar log is
// dominated by pg_doorman output, which Gomega truncates away on failure.
func getWrapperLogs(
	ctx SpecContext,
	clientset *kubernetes.Clientset,
	ns, podName string,
) string {
	var lines []string
	for _, line := range strings.Split(getSidecarLogs(ctx, clientset, ns, podName), "\n") {
		if strings.HasPrefix(line, "{") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// wrapperMetrics scrapes the wrapper metrics endpoint from the postgres
// container: the sidecar image has no HTTP client, so use bash /dev/tcp.
func wrapperMetrics(
	ctx SpecContext,
	clientset *kubernetes.Clientset,
	restConfig *rest.Config,
	ns, podName string,
) (string, error) {
	stdout, _, err := command.ExecuteInContainer(ctx, clientset, restConfig,
		command.ContainerLocator{
			NamespaceName: ns,
			PodName:       podName,
			ContainerName: "postgres",
		},
		nil,
		[]string{"bash", "-c", fmt.Sprintf(
			`exec 3<>/dev/tcp/localhost/%d; printf "GET /metrics HTTP/1.0\r\nHost: localhost\r\n\r\n" >&3; cat <&3`,
			wrapperHealthPort)},
	)
	return stdout, err
}

// metricValue extracts the value of the first sample whose line starts with
// the given metric name and labels.
func metricValue(body, sample string) (float64, bool) {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, sample) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err != nil {
			continue
		}
		return value, true
	}
	return 0, false
}

// initContainerRestartCount returns the restart count of the named sidecar,
// which is injected as a restartable init container.
func initContainerRestartCount(pod *corev1.Pod, name string) int32 {
	for _, status := range pod.Status.InitContainerStatuses {
		if status.Name == name {
			return status.RestartCount
		}
	}
	Fail("no init container status for " + name)
	return 0
}

// The scenarios below mutate the shared plugin Deployment, so they run
// ordered and serially, and AfterAll restores the original image.
var _ = Describe("in-place binary upgrade", Ordered, Serial, func() {
	const (
		configName  = "cr-upgrade"
		clusterName = "test-upgrade"
	)

	var (
		ns         *corev1.Namespace
		cl         client.Client
		clientset  *kubernetes.Clientset
		restConfig *rest.Config
		podName    string
		password   string
		// upgradedSHA is the digest installed by the live upgrade; the pod
		// recreated later must converge on the same one.
		upgradedSHA string
	)

	BeforeAll(func(ctx SpecContext) {
		var err error
		cl, clientset, restConfig, err = internalClient.NewClient()
		Expect(err).NotTo(HaveOccurred())
		ns, err = namespace.CreateUniqueNamespace(ctx, cl, "upgrade")
		Expect(err).NotTo(HaveOccurred())

		By("creating admin password Secret and PgDoorman CR")
		ensureAdminPasswordSecret(ctx, cl, ns.Name, clusterName)
		Expect(cl.Create(ctx, newPgDoorman(ns.Name, configName, clusterName))).To(Succeed())

		By("creating Cluster opted into in-place upgrades")
		Expect(cl.Create(ctx, newClusterWithInPlaceUpgrades(ns.Name, clusterName, configName))).To(Succeed())

		By("waiting for cluster to be ready")
		Eventually(func(g Gomega) {
			var current cnpgv1.Cluster
			g.Expect(cl.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: ns.Name,
			}, &current)).To(Succeed())
			g.Expect(cluster.IsReady(current)).To(BeTrue())
		}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		podName = getPodName(ctx, cl, ns.Name, clusterName)
		password = getAppPassword(ctx, cl, ns.Name, clusterName)

		By("verifying the pooler serves traffic before the upgrade")
		Eventually(func(g Gomega) {
			stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app", "SELECT 1")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(stdout).To(ContainSubstring("1"))
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	})

	AfterAll(func(ctx SpecContext) {
		if cl != nil {
			setPluginImage(ctx, cl, pluginImageCurrent)
		}
		if ns != nil {
			_ = cl.Delete(ctx, ns)
		}
	})

	It("should upgrade pg_doorman in place when the plugin publishes a new binary", func(ctx SpecContext) {
		By("recording the pre-upgrade pod facts")
		var podBefore corev1.Pod
		Expect(cl.Get(ctx, types.NamespacedName{Name: podName, Namespace: ns.Name}, &podBefore)).To(Succeed())
		uidBefore := podBefore.UID
		restartsBefore := initContainerRestartCount(&podBefore, "pg-doorman")
		shaBefore := runtimeBinarySHA(ctx, clientset, restConfig, ns.Name, podName)

		setPluginImage(ctx, cl, pluginImageNext)

		By("waiting for the wrapper to trigger and complete the handover")
		Eventually(func() string {
			return getWrapperLogs(ctx, clientset, ns.Name, podName)
		}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(
			And(
				ContainSubstring("in-place binary upgrade triggered"),
				ContainSubstring("binary upgrade completed, adopted new pg_doorman"),
			),
		)

		By("verifying the pod was neither replaced nor restarted")
		var podAfter corev1.Pod
		Expect(cl.Get(ctx, types.NamespacedName{Name: podName, Namespace: ns.Name}, &podAfter)).To(Succeed())
		Expect(podAfter.UID).To(Equal(uidBefore))
		Expect(initContainerRestartCount(&podAfter, "pg-doorman")).To(Equal(restartsBefore))

		By("verifying the runtime binary was replaced")
		upgradedSHA = runtimeBinarySHA(ctx, clientset, restConfig, ns.Name, podName)
		Expect(upgradedSHA).NotTo(Equal(shaBefore))

		By("verifying the pooler still serves traffic on the new binary")
		Eventually(func(g Gomega) {
			stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app", "SELECT 1")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(stdout).To(ContainSubstring("1"))
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("verifying the wrapper counted a successful upgrade")
		Eventually(func(g Gomega) {
			body, err := wrapperMetrics(ctx, clientset, restConfig, ns.Name, podName)
			g.Expect(err).NotTo(HaveOccurred())
			value, found := metricValue(body, `pg_doorman_wrapper_binary_upgrades_total{result="success"}`)
			g.Expect(found).To(BeTrue(), "success sample not found in wrapper metrics: "+body)
			g.Expect(value).To(BeNumerically(">=", 1))
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	})

	It("should install the desired binary at startup after the pod is recreated", func(ctx SpecContext) {
		Expect(upgradedSHA).NotTo(BeEmpty(), "live upgrade scenario must run first")

		By("deleting the cluster pod")
		var podBefore corev1.Pod
		Expect(cl.Get(ctx, types.NamespacedName{Name: podName, Namespace: ns.Name}, &podBefore)).To(Succeed())
		uidBefore := podBefore.UID
		Expect(cl.Delete(ctx, &podBefore)).To(Succeed())

		By("waiting for the replacement pod")
		Eventually(func(g Gomega) {
			name := getPodName(ctx, cl, ns.Name, clusterName)
			var pod corev1.Pod
			g.Expect(cl.Get(ctx, types.NamespacedName{Name: name, Namespace: ns.Name}, &pod)).To(Succeed())
			g.Expect(pod.UID).NotTo(Equal(uidBefore))
			podName = name
		}).WithTimeout(5 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("waiting for the cluster to be ready again")
		Eventually(func(g Gomega) {
			var current cnpgv1.Cluster
			g.Expect(cl.Get(ctx, types.NamespacedName{
				Name: clusterName, Namespace: ns.Name,
			}, &current)).To(Succeed())
			g.Expect(cluster.IsReady(current)).To(BeTrue())
		}).WithTimeout(10 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())

		By("verifying the wrapper synced the binary before starting pg_doorman")
		Eventually(func() string {
			return getWrapperLogs(ctx, clientset, ns.Name, podName)
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(
			ContainSubstring("desired pg_doorman binary installed before start"),
		)

		By("verifying the runtime binary matches the one delivered by the live upgrade")
		Expect(runtimeBinarySHA(ctx, clientset, restConfig, ns.Name, podName)).To(Equal(upgradedSHA))

		By("verifying the pooler serves traffic on the recreated pod")
		Eventually(func(g Gomega) {
			stdout, _, err := psqlViaPooler(ctx, clientset, restConfig, ns.Name, podName, password, "app", "app", "SELECT 1")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(stdout).To(ContainSubstring("1"))
		}).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	})
})
