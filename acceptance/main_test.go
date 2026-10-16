//go:build acceptance

// Package setupdotnet_test exercises setup-dotnet against real infrastructure:
// real SDK downloads, a real solution built and tested with them, and a real
// CircleCI task-agent when running inside a job. Tagged out of task test
// because it needs network. The CLI contract is covered hermetically by
// clitest/.
package setupdotnet_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/circleci-functions/toolkit/testing/compiler"
)

// binaryPathEnvVar names a prebuilt setup-dotnet to test, for an executor with
// no Go toolchain. The smoke jobs set it, paired with a cross-compiled suite.
const binaryPathEnvVar = "SETUP_DOTNET_BINARY"

var binaryPath string

func TestMain(m *testing.M) {
	status, err := runTests(m)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(status)
}

func runTests(m *testing.M) (int, error) {
	if bin := os.Getenv(binaryPathEnvVar); bin != "" {
		abs, err := filepath.Abs(bin)
		if err != nil {
			return 0, fmt.Errorf("%s=%q: %w", binaryPathEnvVar, bin, err)
		}
		if _, err := os.Stat(abs); err != nil {
			return 0, fmt.Errorf("%s=%q: %w", binaryPathEnvVar, bin, err)
		}
		binaryPath = abs
		return m.Run(), nil
	}

	p := compiler.NewParallel(1)
	defer p.Cleanup()
	p.Add(compiler.Work{Result: &binaryPath, Name: "setup-dotnet", Target: "..", Source: "."})
	if err := p.Run(context.Background()); err != nil {
		return 0, err
	}
	return m.Run(), nil
}

// acceptanceVersion is the dotnet-version the suite installs.
func acceptanceVersion() string {
	if v := os.Getenv("SETUP_DOTNET_ACCEPTANCE_VERSION"); v != "" {
		return v
	}
	return "10.0"
}
