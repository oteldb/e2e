// Package otlp provides OTLP ingest (traces, logs, metrics over gRPC) and thin HTTP query clients
// (Tempo, Loki, Prometheus) so the e2e suite can push real telemetry into oteldb and read it back
// through the same APIs a user would.
package otlp

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otlplog "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	otlpmetric "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	otlptrace "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otellog "go.opentelemetry.io/otel/log"
	apimetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Ingester pushes telemetry to an oteldb OTLP/gRPC endpoint (host:port, plaintext).
type Ingester struct {
	Endpoint string
}

// NewIngester returns an Ingester targeting the given gRPC endpoint (e.g. "127.0.0.1:4317").
func NewIngester(endpoint string) *Ingester { return &Ingester{Endpoint: endpoint} }

func (i *Ingester) res(service string) *resource.Resource {
	return resource.NewSchemaless(
		semconv.ServiceName(service),
	)
}

// EmitTrace sends a small trace (root + child span) tagged with runID and returns the trace ID
// as a lowercase hex string, which the Tempo API can then be queried for by ID.
func (i *Ingester) EmitTrace(ctx context.Context, service, runID string) (string, error) {
	exp, err := otlptrace.New(ctx,
		otlptrace.WithEndpoint(i.Endpoint),
		otlptrace.WithInsecure(),
	)
	if err != nil {
		return "", fmt.Errorf("trace exporter: %w", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp, sdktrace.WithBatchTimeout(time.Millisecond)),
		sdktrace.WithResource(i.res(service)),
	)
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = tp.Shutdown(sctx)
	}()

	tr := tp.Tracer("oteldb-e2e")
	rctx, root := tr.Start(ctx, "e2e-root")
	traceID := root.SpanContext().TraceID().String()
	root.SetAttributes(attribute.String("e2e.run", runID))

	_, child := tr.Start(rctx, "e2e-child")
	child.SetAttributes(attribute.String("e2e.run", runID), attribute.Int("e2e.work", 42))
	time.Sleep(2 * time.Millisecond)
	child.End()
	root.End()

	if err := tp.ForceFlush(ctx); err != nil {
		return "", fmt.Errorf("flush traces: %w", err)
	}
	return traceID, nil
}

// EmitLog sends a single log record whose body embeds runID, under the given service name.
func (i *Ingester) EmitLog(ctx context.Context, service, runID string) error {
	exp, err := otlplog.New(ctx,
		otlplog.WithEndpoint(i.Endpoint),
		otlplog.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("log exporter: %w", err)
	}
	lp := log.NewLoggerProvider(
		log.WithProcessor(log.NewBatchProcessor(exp, log.WithExportInterval(time.Millisecond))),
		log.WithResource(i.res(service)),
	)
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = lp.Shutdown(sctx)
	}()

	logger := lp.Logger("oteldb-e2e")
	var rec otellog.Record
	rec.SetTimestamp(time.Now())
	rec.SetSeverity(otellog.SeverityInfo)
	rec.SetBody(otellog.StringValue("e2e log line run=" + runID))
	rec.AddAttributes(otellog.String("e2e.run", runID))
	logger.Emit(ctx, rec)

	if err := lp.ForceFlush(ctx); err != nil {
		return fmt.Errorf("flush logs: %w", err)
	}
	return nil
}

// EmitMetric records a monotonic counter named "e2e.requests" (exported to Prometheus as
// e2e_requests_total) with a value tagged by runID.
func (i *Ingester) EmitMetric(ctx context.Context, service, runID string, value int64) error {
	exp, err := otlpmetric.New(ctx,
		otlpmetric.WithEndpoint(i.Endpoint),
		otlpmetric.WithInsecure(),
	)
	if err != nil {
		return fmt.Errorf("metric exporter: %w", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(time.Hour))),
		sdkmetric.WithResource(i.res(service)),
	)
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = mp.Shutdown(sctx)
	}()

	counter, err := mp.Meter("oteldb-e2e").Int64Counter("e2e.requests")
	if err != nil {
		return fmt.Errorf("counter: %w", err)
	}
	counter.Add(ctx, value, apimetric.WithAttributes(attribute.String("e2e.run", runID)))

	if err := mp.ForceFlush(ctx); err != nil {
		return fmt.Errorf("flush metrics: %w", err)
	}
	return nil
}
