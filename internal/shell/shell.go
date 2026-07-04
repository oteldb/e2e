// Package shell wraps external command execution (docker, kind, kubectl, go, make) used by the
// e2e harness. It mirrors the operator's test/utils.Run: commands are logged to the Ginkgo
// writer and their combined output is captured so failures carry the full context.
package shell

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	. "github.com/onsi/ginkgo/v2" //nolint:revive,staticcheck // dot-import is the Ginkgo idiom.
)

// Run executes cmd, streaming a log line to the Ginkgo writer, and returns its combined output.
func Run(cmd *exec.Cmd) (string, error) {
	command := strings.Join(cmd.Args, " ")
	if cmd.Dir != "" {
		_, _ = fmt.Fprintf(GinkgoWriter, "running (in %s): %q\n", cmd.Dir, command)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "running: %q\n", command)
	}
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%q failed: %w\n%s", command, err, string(out))
	}
	return string(out), nil
}

// Command builds an exec.Cmd for name with args, inheriting the current environment.
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	return cmd
}

// InDir builds a Command that runs in dir.
func InDir(dir, name string, args ...string) *exec.Cmd {
	cmd := Command(name, args...)
	cmd.Dir = dir
	return cmd
}

// Output runs name with args and returns trimmed stdout+stderr, discarding trailing whitespace.
func Output(name string, args ...string) (string, error) {
	out, err := Run(Command(name, args...))
	return strings.TrimSpace(out), err
}

// Getenv returns the environment value for key, or def when unset/empty.
func Getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Truthy reports whether an environment variable is set to a truthy value (1/true/yes/on).
func Truthy(key string) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
