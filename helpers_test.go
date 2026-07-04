package e2e

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/oteldb/e2e/internal/kubeutil"
	"github.com/oteldb/e2e/internal/otlp"
)

var idCounter atomic.Int64

// randID returns a short unique id used to tag telemetry so specs don't collide. It combines a
// process-start-relative counter with the current unix time.
func randID() string {
	return fmt.Sprintf("%d-%d", time.Now().Unix(), idCounter.Add(1))
}

// oteldb API ports (must match the operator's exposed container/service ports).
const (
	portOTLPGRPC = 4317
	portProm     = 9090
	portLoki     = 3100
	portTempo    = 3200
)

// endpoints establishes a fresh port-forward to the OtelDBCluster's client Service and returns an
// OTLP ingester plus read-side query clients. A cleanup that stops the forward is registered on
// the current spec via DeferCleanup, so callers get a clean forward each time — important after
// chaos, since a Service port-forward binds to a single backing pod.
func endpoints() (*otlp.Ingester, *otlp.Clients) {
	svc := "service/" + conf.ClusterName
	ports, stop, err := kubeutil.PortForward(conf.Namespace, svc,
		portOTLPGRPC, portProm, portLoki, portTempo)
	Expect(err).NotTo(HaveOccurred(), "Failed to port-forward to oteldb")
	DeferCleanup(stop)

	ingest := otlp.NewIngester(ports[portOTLPGRPC])
	clients := otlp.NewClients(ports[portTempo], ports[portLoki], ports[portProm])
	return ingest, clients
}

// ingestAndVerify pushes a trace, a log and a metric tagged with runID and asserts each is
// queryable back through the Tempo, Loki and Prometheus APIs. Returns once all three are visible.
func ingestAndVerify(ctx context.Context, ingest *otlp.Ingester, clients *otlp.Clients, runID string) {
	By(fmt.Sprintf("ingesting telemetry (run=%s)", runID))
	traceID, err := ingest.EmitTrace(ctx, "oteldb-e2e-traces", runID)
	Expect(err).NotTo(HaveOccurred(), "Failed to emit trace")
	Expect(ingest.EmitLog(ctx, "oteldb-e2e-logs", runID)).To(Succeed(), "Failed to emit log")
	Expect(ingest.EmitMetric(ctx, "oteldb-e2e-metrics", runID, 7)).To(Succeed(), "Failed to emit metric")

	By("querying the trace back via the Tempo API")
	Eventually(func() (bool, error) {
		return clients.TraceExists(ctx, traceID)
	}).Should(BeTrue(), "trace %s not queryable", traceID)

	By("querying the log back via the Loki API")
	Eventually(func() (bool, error) {
		return clients.LogsContain(ctx, `{service_name="oteldb-e2e-logs"}`, runID)
	}).Should(BeTrue(), "log line for run=%s not queryable", runID)

	By("querying the metric back via the Prometheus API")
	// oteldb sanitizes the OTLP name "e2e.requests" to a Prometheus label (dots -> underscores);
	// a "_total" suffix may or may not be appended. Match either form via a regex on __name__.
	Eventually(func() (bool, error) {
		return clients.SeriesExists(ctx, `{__name__=~"e2e.requests.*"}`)
	}).Should(BeTrue(), "metric e2e_requests not queryable")
}
