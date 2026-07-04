# oteldb e2e

[![e2e](https://github.com/oteldb/e2e/actions/workflows/e2e.yml/badge.svg)](https://github.com/oteldb/e2e/actions/workflows/e2e.yml)

End-to-end tests for [`oteldb`](../oteldb) driven through the
[`operator`](../operator), running on a real (in-Docker) Kubernetes cluster via
[kind](https://kind.sigs.k8s.io/).

The suite:

1. **Builds from local source (fast path).** Compiles `oteldb` (`./cmd/oteldb`) and the operator
   manager (`./cmd`) on the host — reusing the warm Go build cache — then packages each into a
   minimal image (`fast.Dockerfile`, a single `COPY` layer) and `kind load`s it. A rebuild after a
   local edit to `/src/oteldb/oteldb` is a `go build` + one COPY, not an in-container module
   download and full multi-stage build.
2. **Deploys the operator** (CRDs + controller-manager) via the operator's own Makefile. There are
   no webhooks, so cert-manager is not required.
3. **Brings up a clustered `OtelDBCluster`** — 3 symmetric nodes on the per-node `file` backend
   with `replicationFactor: 2`, backed by a bring-your-own dev etcd (`manifests/etcd-dev.yaml`).
4. **Exercises the normal path**: ingests traces, logs and metrics over OTLP/gRPC and reads each
   back through its native query API (Tempo `/api/traces`, Loki `/loki/api/v1/query_range`,
   Prometheus `/api/v1/query`).
5. **Exercises chaos-injection paths**:
   - **Pod kill** — force-delete a node (its PVC is retained); assert previously-ingested data
     stays queryable (it is restored from the node's own WAL/parts on the PVC), the cluster
     returns to Ready, and fresh telemetry ingests/queries.
   - **Disk loss** — delete a node *and its PVC*; assert the cluster self-heals (the wiped node
     rejoins with a fresh PVC and the cluster returns to Ready) and stays available (new telemetry
     ingests/queries across all signals).

   > **Durability note.** With the per-node `file` backend, a node's data lives on its own PVC
   > (WAL + parts). RF replication and read fan-out provide write-durability and availability while
   > a peer is transiently down, but reads are primary-authoritative: a node returning under the
   > same identity with an *empty* disk is not auto-restored from replicas within the query window.
   > That disaster-recovery case is what the shared S3 backend addresses — which is intentionally
   > **out of scope here** (file backend only). So the disk-loss spec asserts self-healing +
   > availability, not recovery of the wiped node's historical data.

## Requirements

`docker`, `kind`, `kubectl`, and a Go toolchain — all on `PATH`. The sibling `../oteldb` and
`../operator` source trees must be present (override with env vars below).

## Running

```bash
make test          # full run: build + fresh kind cluster + all specs + teardown
make test-keep     # same, but leave the cluster up for inspection
make test-fast     # rebuild oteldb from source, reuse an existing cluster (fast iteration)
make test-no-build # reuse cluster AND already-loaded images (fastest; just re-run specs)
make cluster       # create the kind cluster only
make clean         # delete the kind cluster
```

Fast local loop while hacking on `/src/oteldb/oteldb`:

```bash
make cluster       # once
make test-fast     # rebuild + reload oteldb, redeploy CR, re-run — repeat on each edit
```

## Configuration (env vars)

| Var | Default | Meaning |
|-----|---------|---------|
| `OTELDB_SRC` | `/src/oteldb/oteldb` | oteldb source tree to build |
| `OPERATOR_SRC` | `/src/oteldb/operator` | operator source tree to build/deploy |
| `OTELDB_IMAGE` | `oteldb/oteldb:e2e` | built oteldb image tag (must match `manifests/cluster.yaml` `spec.image`) |
| `OPERATOR_IMAGE` | `oteldb/operator:e2e` | built operator image tag |
| `KIND_CLUSTER` | `oteldb-e2e` | kind cluster name |
| `KIND_CONFIG` | `kind.yaml` | kind cluster config |
| `E2E_NAMESPACE` | `oteldb-e2e` | namespace for the cluster + etcd |
| `E2E_CLUSTER_NAME` | `oteldb` | `OtelDBCluster` name |
| `E2E_SKIP_BUILD` | _unset_ | reuse already-loaded images (skip host compile + load) |
| `E2E_REUSE_CLUSTER` | _unset_ | assume the kind cluster already exists (no create/delete) |
| `E2E_KEEP_CLUSTER` | _unset_ | leave the kind cluster running after the suite |

## Layout

```
manifests/
  etcd-dev.yaml     dev etcd (single replica, emptyDir) — bring-your-own coordination
  cluster.yaml      the OtelDBCluster under test (file backend, RF=2, image pinned)
fast.Dockerfile     minimal image wrapping a host-built binary (fast rebuild path)
kind.yaml           1 control-plane + 3 workers (room to spread replicas + survive a node loss)
internal/
  cfg/              env-driven suite configuration
  shell/            command execution wrappers (docker/kind/kubectl/go/make)
  build/            fast image path: host go build -> COPY image -> kind load
  kubeutil/         kubectl helpers: apply/wait/list/delete + Service port-forward
  otlp/             OTLP/gRPC ingest (traces/logs/metrics) + Tempo/Loki/Prometheus query clients
suite_test.go       Ginkgo bootstrap: kind + build/load + deploy operator + bring up cluster
helpers_test.go     shared port-forward + ingest/verify helpers
normal_test.go      happy-path ingest/query specs
chaos_test.go       pod-kill and disk-loss recovery specs
```
