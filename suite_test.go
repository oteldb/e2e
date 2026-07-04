package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/oteldb/e2e/internal/build"
	"github.com/oteldb/e2e/internal/cfg"
	"github.com/oteldb/e2e/internal/kubeutil"
	"github.com/oteldb/e2e/internal/shell"
)

// conf holds the resolved suite configuration, shared across specs.
var conf cfg.Config

// anyFailed records whether any spec failed, so AfterSuite can dump diagnostics once.
var anyFailed bool

// setupComplete is set at the end of BeforeSuite; if AfterSuite sees it false, setup failed and
// diagnostics are dumped.
var setupComplete bool

var _ = ReportAfterEach(func(report SpecReport) {
	if report.Failed() {
		anyFailed = true
	}
})

// selector matches the oteldb pods managed by the operator for our OtelDBCluster.
func selector() string {
	return "app.kubernetes.io/name=oteldb,app.kubernetes.io/instance=" + conf.ClusterName
}

// TestE2E is the entrypoint for the oteldb e2e suite. It runs against an isolated kind cluster:
// it builds oteldb (and the operator) from local source, loads them into kind, deploys the
// operator, brings up a clustered OtelDBCluster on the per-node file backend, then exercises
// realistic ingest/query paths and chaos (pod kill, disk loss) recovery.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting oteldb e2e suite\n")
	RunSpecs(t, "oteldb e2e suite")
}

var _ = BeforeSuite(func() {
	conf = cfg.Load()
	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(2 * time.Second)

	ensureKindCluster()

	if conf.SkipBuild {
		By("skipping image build (E2E_SKIP_BUILD set); reusing loaded images")
	} else {
		By("building and loading the oteldb image from local source (fast path)")
		Expect(build.BinaryImage(conf.OteldbSrc, "./cmd/oteldb", "oteldb", conf.OteldbImage, conf.KindCluster)).
			To(Succeed(), "Failed to build/load oteldb image")

		By("building and loading the operator image from local source (fast path)")
		Expect(build.BinaryImage(conf.OperatorSrc, "./cmd", "manager", conf.OperatorImage, conf.KindCluster)).
			To(Succeed(), "Failed to build/load operator image")
	}

	deployOperator()
	deployEtcdAndCluster()
	setupComplete = true
})

var _ = AfterSuite(func() {
	if anyFailed || !setupComplete {
		dumpDiagnostics()
	}
	if conf.ReuseCluster || conf.KeepCluster {
		By(fmt.Sprintf("leaving kind cluster %q in place", conf.KindCluster))
		return
	}
	By(fmt.Sprintf("deleting kind cluster %q", conf.KindCluster))
	_, _ = shell.Run(shell.Command("kind", "delete", "cluster", "--name", conf.KindCluster))
})

// ensureKindCluster creates the kind cluster from kind.yaml unless it already exists or the suite
// is configured to reuse an external cluster.
func ensureKindCluster() {
	clusters, _ := shell.Output("kind", "get", "clusters")
	exists := false
	for _, c := range strings.Fields(clusters) {
		if c == conf.KindCluster {
			exists = true
			break
		}
	}
	if conf.ReuseCluster {
		Expect(exists).To(BeTrue(),
			fmt.Sprintf("E2E_REUSE_CLUSTER set but kind cluster %q does not exist", conf.KindCluster))
		By(fmt.Sprintf("reusing existing kind cluster %q", conf.KindCluster))
		return
	}
	if exists {
		By(fmt.Sprintf("kind cluster %q already exists; reusing", conf.KindCluster))
		return
	}
	By(fmt.Sprintf("creating kind cluster %q", conf.KindCluster))
	_, err := shell.Run(shell.Command("kind", "create", "cluster",
		"--name", conf.KindCluster, "--config", conf.KindConfig, "--wait", "120s"))
	Expect(err).NotTo(HaveOccurred(), "Failed to create kind cluster")
}

// deployOperator installs the CRDs and controller-manager via the operator's Makefile. No webhooks
// exist, so cert-manager is skipped.
func deployOperator() {
	By("deploying the oteldb operator (CRDs + controller-manager)")
	deploy := shell.InDir(conf.OperatorSrc, "make", "deploy", "IMG="+conf.OperatorImage)
	deploy.Env = append(os.Environ(), "CERT_MANAGER_INSTALL_SKIP=true")
	_, err := shell.Run(deploy)
	Expect(err).NotTo(HaveOccurred(), "Failed to deploy operator")

	By("waiting for the controller-manager to become Available")
	Expect(kubeutil.WaitAvailable(conf.OperatorNamespace, "operator-controller-manager", 3*time.Minute)).
		To(Succeed(), "controller-manager did not become Available")
}

// deployEtcdAndCluster stands up the dev etcd and the OtelDBCluster, then waits for the
// StatefulSet to be fully rolled out and the CR to report Ready.
func deployEtcdAndCluster() {
	By("creating the test namespace")
	Expect(kubeutil.EnsureNamespace(conf.Namespace)).To(Succeed())

	By("deploying the dev etcd")
	Expect(kubeutil.Apply(conf.Namespace, "manifests/etcd-dev.yaml")).To(Succeed())
	Expect(kubeutil.WaitRollout(conf.Namespace, "statefulset/etcd", 2*time.Minute)).
		To(Succeed(), "etcd did not become ready")

	By("applying the OtelDBCluster")
	Expect(kubeutil.Apply(conf.Namespace, "manifests/cluster.yaml")).To(Succeed())

	By("waiting for the oteldb StatefulSet rollout")
	Expect(kubeutil.WaitRollout(conf.Namespace, "statefulset/"+conf.ClusterName, 5*time.Minute)).
		To(Succeed(), "oteldb StatefulSet did not roll out")

	By("waiting for the OtelDBCluster to report Ready")
	Eventually(func() (string, error) {
		return kubeutil.ClusterPhase(conf.Namespace, conf.ClusterName)
	}, 3*time.Minute, 3*time.Second).Should(Equal("Ready"), "OtelDBCluster did not reach Ready phase")
}

// dumpDiagnostics prints operator logs, oteldb pod state and events to aid debugging.
func dumpDiagnostics() {
	By("collecting diagnostics")
	if out, err := kubeutil.Kubectl("get", "pods", "-n", conf.Namespace, "-o", "wide"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "oteldb pods:\n%s\n", out)
	}
	if out, err := kubeutil.Kubectl("get", "oteldbcluster", conf.ClusterName, "-n", conf.Namespace, "-o", "yaml"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "OtelDBCluster:\n%s\n", out)
	}
	if out, err := kubeutil.Kubectl("get", "pods", "-n", conf.OperatorNamespace, "-o", "wide"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "operator pods:\n%s\n", out)
	}
	if out, err := kubeutil.Kubectl("describe", "pods", "-n", conf.OperatorNamespace,
		"-l", "control-plane=controller-manager"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "operator pod describe:\n%s\n", out)
	}
	if out, err := kubeutil.Kubectl("logs", "-n", conf.OperatorNamespace,
		"deployment/operator-controller-manager", "--tail=200"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "operator logs:\n%s\n", out)
	}
	if out, err := kubeutil.Kubectl("get", "events", "-n", conf.Namespace,
		"--sort-by=.lastTimestamp"); err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "events:\n%s\n", out)
	}
}
