// Package cfg centralizes the environment-driven knobs for the e2e suite so the fast local
// iteration path (rebuild oteldb from /src/oteldb/oteldb, reload into kind, redeploy) is a matter
// of a couple of env vars rather than code edits.
package cfg

import "github.com/oteldb/e2e/internal/shell"

// Config holds the resolved suite configuration.
type Config struct {
	// OteldbSrc is the local oteldb server source tree that the fast build path compiles.
	OteldbSrc string
	// OperatorSrc is the local operator source tree (Makefile-driven deploy).
	OperatorSrc string

	// OteldbImage / OperatorImage are the locally built, kind-loaded image tags. They MUST match
	// spec.image in manifests/cluster.yaml (oteldb) and the operator deploy IMG respectively.
	OteldbImage   string
	OperatorImage string

	// KindCluster is the kind cluster name.
	KindCluster string
	// KindConfig is the path to the kind cluster config (multi-node).
	KindConfig string

	// Namespace is where the OtelDBCluster and its dev etcd live.
	Namespace string
	// ClusterName is the OtelDBCluster metadata.name (== the StatefulSet/Service name).
	ClusterName string
	// OperatorNamespace is where the controller-manager is deployed by `make deploy`.
	OperatorNamespace string

	// SkipBuild reuses already-loaded images (skip host compile + kind load).
	SkipBuild bool
	// KeepCluster leaves the kind cluster running after the suite (for debugging).
	KeepCluster bool
	// ReuseCluster assumes the kind cluster already exists (no create, no delete).
	ReuseCluster bool
}

// Load resolves the suite config from the environment, applying defaults.
func Load() Config {
	return Config{
		OteldbSrc:         shell.Getenv("OTELDB_SRC", "/src/oteldb/oteldb"),
		OperatorSrc:       shell.Getenv("OPERATOR_SRC", "/src/oteldb/operator"),
		OteldbImage:       shell.Getenv("OTELDB_IMAGE", "oteldb/oteldb:e2e"),
		OperatorImage:     shell.Getenv("OPERATOR_IMAGE", "oteldb/operator:e2e"),
		KindCluster:       shell.Getenv("KIND_CLUSTER", "oteldb-e2e"),
		KindConfig:        shell.Getenv("KIND_CONFIG", "kind.yaml"),
		Namespace:         shell.Getenv("E2E_NAMESPACE", "oteldb-e2e"),
		ClusterName:       shell.Getenv("E2E_CLUSTER_NAME", "oteldb"),
		OperatorNamespace: shell.Getenv("OPERATOR_NAMESPACE", "operator-system"),
		SkipBuild:         shell.Truthy("E2E_SKIP_BUILD"),
		KeepCluster:       shell.Truthy("E2E_KEEP_CLUSTER"),
		ReuseCluster:      shell.Truthy("E2E_REUSE_CLUSTER"),
	}
}
