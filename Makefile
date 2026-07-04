# e2e — end-to-end tests for oteldb + operator on kind.
#
# The suite builds oteldb (and the operator) from local source, loads them into a kind cluster,
# deploys the operator, brings up a clustered OtelDBCluster (file backend, RF=2), and exercises
# realistic ingest/query and chaos-recovery paths. See README.md.

SHELL := /bin/bash

KIND_CLUSTER   ?= oteldb-e2e
OTELDB_SRC     ?= /src/oteldb/oteldb
OPERATOR_SRC   ?= /src/oteldb/operator
OTELDB_IMAGE   ?= oteldb/oteldb:e2e
OPERATOR_IMAGE ?= oteldb/operator:e2e

# Ginkgo verbosity and a generous per-suite timeout (image builds + cluster bring-up + chaos).
GO_TEST_FLAGS ?= -v -timeout 45m -ginkgo.v

export KIND_CLUSTER OTELDB_SRC OPERATOR_SRC OTELDB_IMAGE OPERATOR_IMAGE

.PHONY: test
test: ## Run the full e2e suite (creates a kind cluster, tears it down after).
	go test ./... $(GO_TEST_FLAGS)

.PHONY: test-keep
test-keep: ## Run the suite but leave the kind cluster running for inspection.
	E2E_KEEP_CLUSTER=true go test ./... $(GO_TEST_FLAGS)

.PHONY: test-fast
test-fast: ## Rebuild oteldb from local source and re-run against the EXISTING kind cluster.
	E2E_REUSE_CLUSTER=true go test ./... $(GO_TEST_FLAGS)

.PHONY: test-no-build
test-no-build: ## Re-run against the existing cluster WITHOUT rebuilding/reloading images.
	E2E_REUSE_CLUSTER=true E2E_SKIP_BUILD=true go test ./... $(GO_TEST_FLAGS)

.PHONY: cluster
cluster: ## Create the kind cluster only.
	kind create cluster --name $(KIND_CLUSTER) --config kind.yaml --wait 120s

.PHONY: clean
clean: ## Delete the kind cluster.
	kind delete cluster --name $(KIND_CLUSTER)

.PHONY: vet
vet: ## Vet the module.
	go vet ./...

.PHONY: help
help: ## Show this help.
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-16s\033[0m %s\n", $$1, $$2}'
