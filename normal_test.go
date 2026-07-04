package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/oteldb/e2e/internal/kubeutil"
)

// The normal (non-chaos) path: with a healthy 3-node cluster, all three signals ingest over OTLP
// and read back through their native query APIs.
var _ = Describe("Normal path", func() {
	It("ingests and queries traces, logs and metrics", func(ctx SpecContext) {
		ingest, clients := endpoints()
		ingestAndVerify(ctx, ingest, clients, "normal-"+randID())
	})

	It("keeps all replicas Ready", func() {
		ready, err := kubeutil.ReadyPodCount(conf.Namespace, selector())
		Expect(err).NotTo(HaveOccurred())
		Expect(ready).To(Equal(3), "expected all 3 oteldb replicas Ready")
	})

	It("serves the Prometheus query API across the cluster Service", func(ctx SpecContext) {
		_, clients := endpoints()
		// up-style liveness: the API answers a trivial PromQL instant query.
		Eventually(func() (bool, error) {
			return clients.SeriesExists(ctx, `vector(1)`)
		}).Should(BeTrue(), "Prometheus API did not answer a trivial query")
	})
})
