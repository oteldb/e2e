package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/oteldb/e2e/internal/kubeutil"
)

// Chaos-injection paths. The cluster runs the per-node file backend with replicationFactor 2, so
// losing one node (pod, or pod + its disk) must not lose data or availability: a surviving replica
// holds a copy, and the StatefulSet reschedules the lost node, which re-joins the ring and
// re-replicates. Each spec ingests its own uniquely-tagged telemetry so it is order-independent.
var _ = Describe("Chaos", func() {
	// waitClusterHealthy blocks until all 3 replicas are Ready again and the CR reports Ready.
	waitClusterHealthy := func() {
		By("waiting for the StatefulSet to become fully Ready again")
		Eventually(func() (int, error) {
			return kubeutil.ReadyPodCount(conf.Namespace, selector())
		}, 5*time.Minute, 5*time.Second).Should(Equal(3), "cluster did not return to 3 Ready replicas")

		By("waiting for the OtelDBCluster to report Ready again")
		Eventually(func() (string, error) {
			return kubeutil.ClusterPhase(conf.Namespace, conf.ClusterName)
		}, 3*time.Minute, 3*time.Second).Should(Equal("Ready"))
	}

	It("survives an abrupt pod kill without losing data or availability", func(ctx SpecContext) {
		runID := "chaos-kill-" + randID()

		By("ingesting telemetry before the disruption")
		ingest, clients := endpoints()
		ingestAndVerify(ctx, ingest, clients, runID)

		By("force-deleting one oteldb pod")
		pods, err := kubeutil.PodNames(conf.Namespace, selector())
		Expect(err).NotTo(HaveOccurred())
		Expect(pods).To(HaveLen(3))
		victim := pods[0]
		Expect(kubeutil.DeletePodNow(conf.Namespace, victim)).To(Succeed())

		By("verifying the cluster still serves the previously-ingested data during recovery")
		// The client Service routes only to Ready pods; a fresh port-forward lands on a survivor.
		_, survivorClients := endpoints()
		Eventually(func() (bool, error) {
			return survivorClients.LogsContain(ctx, `{service_name="oteldb-e2e-logs"}`, runID)
		}).Should(BeTrue(), "data ingested before the kill became unqueryable")

		waitClusterHealthy()

		By("ingesting and querying fresh telemetry after recovery")
		ingest2, clients2 := endpoints()
		ingestAndVerify(ctx, ingest2, clients2, runID+"-after")
	})

	// Disk loss (a node's local PersistentVolume is wiped) vs. pod loss (above) exercises a
	// different guarantee. With the per-node `file` backend, a node's data lives on its own PVC
	// (WAL + parts); RF replication and read fan-out provide write-durability and availability
	// while a peer is transiently down, but reads are primary-authoritative — a node that returns
	// under the same identity with an empty disk is NOT auto-restored from replicas within the
	// query window (that recovery is the shared S3 backend's job, and out of scope here). So the
	// guarantee this spec asserts is self-healing + continued availability: the wiped node
	// rejoins with a fresh PVC, the cluster returns to Ready, and it keeps ingesting and serving.
	It("self-heals and stays available after a node loses its local disk (PVC)", func(ctx SpecContext) {
		runID := "chaos-disk-" + randID()

		By("establishing a baseline before the disruption")
		ingest, clients := endpoints()
		ingestAndVerify(ctx, ingest, clients, runID)

		By("deleting one oteldb pod together with its PersistentVolumeClaim (disk loss)")
		pods, err := kubeutil.PodNames(conf.Namespace, selector())
		Expect(err).NotTo(HaveOccurred())
		Expect(pods).To(HaveLen(3))
		victim := pods[len(pods)-1]
		// The per-pod PVC is named "<dataVolume>-<pod>" == "data-<pod>" (StatefulSet convention).
		// Mark the PVC for deletion first (non-blocking; it is pinned by the running pod's
		// pvc-protection finalizer), then force-delete the pod so the finalizer clears and the
		// StatefulSet re-provisions a fresh, empty PVC.
		Expect(kubeutil.DeletePVC(conf.Namespace, "data-"+victim)).To(Succeed())
		Expect(kubeutil.DeletePodNow(conf.Namespace, victim)).To(Succeed())

		waitClusterHealthy()

		By("verifying the recovered cluster accepts and serves new telemetry across all signals")
		ingest2, clients2 := endpoints()
		ingestAndVerify(ctx, ingest2, clients2, runID+"-after")
	})
})
