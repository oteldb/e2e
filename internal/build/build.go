// Package build implements the fast image path for the e2e suite: compile a Go binary on the host
// (reusing the warm build cache), package it into a minimal image via fast.Dockerfile, and load
// it into the kind cluster. Rebuilding after a local edit to /src/oteldb/oteldb is a `go build`
// plus a single-COPY docker build — seconds on a warm cache — instead of an in-container module
// download and full multi-stage build.
package build

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck // dot-import is the Ginkgo idiom.

	"github.com/oteldb/e2e/internal/shell"
)

// fastDockerfile is resolved relative to the suite root at runtime.
const fastDockerfile = "fast.Dockerfile"

// BinaryImage compiles a command package from srcDir into a linux binary, packages it with
// fast.Dockerfile as image, and loads it into the named kind cluster.
//
//   - srcDir:  the Go module root to build from (e.g. /src/oteldb/oteldb).
//   - cmdPkg:  the command package to build (e.g. "./cmd/oteldb" or "./cmd").
//   - binName: the resulting binary/COPY name (e.g. "oteldb" or "manager").
//   - image:   the resulting image tag (e.g. oteldb/oteldb:e2e).
//   - kindCluster: the kind cluster to `kind load docker-image` into.
func BinaryImage(srcDir, cmdPkg, binName, image, kindCluster string) error {
	ctxDir, err := os.MkdirTemp("", "oteldb-e2e-build-*")
	if err != nil {
		return fmt.Errorf("create build context dir: %w", err)
	}
	defer os.RemoveAll(ctxDir)

	binPath := filepath.Join(ctxDir, binName)
	By(fmt.Sprintf("compiling %s from %s (host cache)", binName, srcDir))
	buildCmd := shell.InDir(srcDir, "go", "build",
		"-trimpath", "-buildvcs=false",
		"-o", binPath, cmdPkg,
	)
	// Static linux binary regardless of host toolchain settings; kind nodes are linux/<host arch>.
	buildCmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS=linux",
		"GOARCH="+runtime.GOARCH,
	)
	if _, err := shell.Run(buildCmd); err != nil {
		return fmt.Errorf("build %s: %w", binName, err)
	}

	dockerfile, err := filepath.Abs(fastDockerfile)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", fastDockerfile, err)
	}

	By(fmt.Sprintf("packaging %s into %s", binName, image))
	dockerBuild := shell.Command("docker", "build",
		"-f", dockerfile,
		"--build-arg", "BIN="+binName,
		"-t", image,
		ctxDir,
	)
	if _, err := shell.Run(dockerBuild); err != nil {
		return fmt.Errorf("docker build %s: %w", image, err)
	}

	By(fmt.Sprintf("loading %s into kind cluster %s", image, kindCluster))
	load := shell.Command("kind", "load", "docker-image", image, "--name", kindCluster)
	if _, err := shell.Run(load); err != nil {
		return fmt.Errorf("kind load %s: %w", image, err)
	}
	return nil
}
