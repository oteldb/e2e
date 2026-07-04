// Package kubeutil wraps the kubectl operations the e2e suite needs: applying manifests, waiting
// for rollouts, listing/deleting pods and PVCs (chaos), and establishing port-forwards to reach
// the in-cluster oteldb APIs from the test process.
package kubeutil

import (
	"bufio"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck // dot-import is the Ginkgo idiom.

	"github.com/oteldb/e2e/internal/shell"
)

// Kubectl runs a kubectl command and returns its combined output.
func Kubectl(args ...string) (string, error) {
	return shell.Run(shell.Command("kubectl", args...))
}

// Apply applies a manifest file into a namespace (namespace "" = manifest-defined).
func Apply(namespace, path string) error {
	args := []string{"apply", "-f", path}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	_, err := Kubectl(args...)
	return err
}

// Delete deletes the resources in a manifest file, ignoring not-found.
func Delete(namespace, path string) error {
	args := []string{"delete", "-f", path, "--ignore-not-found"}
	if namespace != "" {
		args = append(args, "-n", namespace)
	}
	_, err := Kubectl(args...)
	return err
}

// EnsureNamespace creates the namespace if it does not already exist.
func EnsureNamespace(ns string) error {
	if _, err := Kubectl("get", "namespace", ns); err == nil {
		return nil
	}
	_, err := Kubectl("create", "namespace", ns)
	return err
}

// WaitRollout blocks until a StatefulSet/Deployment rollout completes within timeout.
func WaitRollout(namespace, kindName string, timeout time.Duration) error {
	_, err := Kubectl("rollout", "status", kindName,
		"-n", namespace, fmt.Sprintf("--timeout=%s", timeout))
	return err
}

// WaitAvailable waits for a deployment to report Available (used for the operator manager).
func WaitAvailable(namespace, deployment string, timeout time.Duration) error {
	_, err := Kubectl("wait", "deployment/"+deployment,
		"--for=condition=Available", "-n", namespace, fmt.Sprintf("--timeout=%s", timeout))
	return err
}

// PodNames returns the names of pods matching a label selector in a namespace.
func PodNames(namespace, selector string) ([]string, error) {
	out, err := Kubectl("get", "pods", "-n", namespace, "-l", selector,
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\n\"}{end}")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// ReadyPodCount returns how many pods matching selector have Ready=True.
func ReadyPodCount(namespace, selector string) (int, error) {
	out, err := Kubectl("get", "pods", "-n", namespace, "-l", selector,
		"-o", "jsonpath={range .items[*]}{.status.conditions[?(@.type==\"Ready\")].status}{\"\\n\"}{end}")
	if err != nil {
		return 0, err
	}
	ready := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "True" {
			ready++
		}
	}
	return ready, nil
}

// DeletePodNow force-deletes a pod with no grace period (simulates an abrupt node/pod failure).
func DeletePodNow(namespace, pod string) error {
	_, err := Kubectl("delete", "pod", pod, "-n", namespace,
		"--grace-period=0", "--force")
	return err
}

// DeletePVC marks a PersistentVolumeClaim for deletion (simulates losing a node's local disk).
// It uses --wait=false because a bound PVC carries the pvc-protection finalizer and will not
// actually delete until the pod mounting it is gone: the caller deletes the PVC first (marking it
// Terminating) and then force-deletes the pod, after which the finalizer clears and the
// StatefulSet re-provisions a fresh, empty PVC. A blocking delete here would deadlock.
func DeletePVC(namespace, pvc string) error {
	_, err := Kubectl("delete", "pvc", pvc, "-n", namespace, "--ignore-not-found", "--wait=false")
	return err
}

// ClusterPhase reads .status.phase of an OtelDBCluster.
func ClusterPhase(namespace, name string) (string, error) {
	return shell.Output("kubectl", "get", "oteldbcluster", name,
		"-n", namespace, "-o", "jsonpath={.status.phase}")
}

// Describe dumps a resource description (used for failure diagnostics).
func DescribeResource(namespace, resource string) string {
	out, _ := Kubectl("describe", resource, "-n", namespace)
	return out
}

// forwardLine matches kubectl port-forward's "Forwarding from 127.0.0.1:<local> -> <remote>".
var forwardLine = regexp.MustCompile(`Forwarding from 127\.0\.0\.1:(\d+) -> (\d+)`)

// PortForward starts `kubectl port-forward` to a target (e.g. "service/oteldb") requesting a
// random local port for each remote port. It returns a map remotePort->local address
// ("127.0.0.1:<port>") and a stop function. The caller must invoke stop (e.g. via DeferCleanup).
//
// Forwarding to a Service targets a single ready backing pod; after a chaos spec kills pods,
// re-establish the forward so it lands on a survivor.
func PortForward(namespace, target string, remotePorts ...int) (map[int]string, func(), error) {
	args := []string{"port-forward", target, "-n", namespace}
	for _, p := range remotePorts {
		args = append(args, fmt.Sprintf(":%d", p)) // leading colon => OS-chosen local port
	}
	cmd := exec.Command("kubectl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	cmd.Stderr = GinkgoWriter
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start port-forward: %w", err)
	}
	stop := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}

	mapping := make(map[int]string, len(remotePorts))
	done := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			_, _ = fmt.Fprintf(GinkgoWriter, "port-forward: %s\n", line)
			if m := forwardLine.FindStringSubmatch(line); m != nil {
				var local, remote int
				_, _ = fmt.Sscanf(m[1], "%d", &local)
				_, _ = fmt.Sscanf(m[2], "%d", &remote)
				mapping[remote] = fmt.Sprintf("127.0.0.1:%d", local)
				if len(mapping) == len(remotePorts) {
					close(done)
				}
			}
		}
	}()

	select {
	case <-done:
		return mapping, stop, nil
	case <-time.After(30 * time.Second):
		stop()
		return nil, nil, fmt.Errorf("timed out establishing port-forward to %s (%v)", target, remotePorts)
	}
}
