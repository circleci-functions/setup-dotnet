//go:build acceptance

package setupdotnet_test

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/pathlink"
	"github.com/circleci-functions/toolkit/sdk/sdktest"
	"github.com/circleci-functions/toolkit/testing/runner"
)

// job runs setup-dotnet in a sandbox whose bin directory comes first on the
// executor's real PATH, so what it links is what a later step would run.
type job struct {
	t       *testing.T
	sandbox *sdktest.Sandbox
	wd      string
	path    string
}

func newJob(t *testing.T) *job {
	t.Helper()
	s := sdktest.NewSandbox(t, sdktest.WithLiveCache())
	return &job{t: t, sandbox: s, wd: t.TempDir(), path: s.BinDir + string(os.PathListSeparator) + os.Getenv("PATH")}
}

func (j *job) run(args ...string) string {
	j.t.Helper()
	env := j.sandbox.Env()
	maps.Copy(env, map[string]string{"PATH": j.path, "DOTNET_CLI_TELEMETRY_OPTOUT": "1"})
	for _, name := range []string{"USERPROFILE", "LOCALAPPDATA", "APPDATA", "SystemRoot", "ProgramFiles", "CIRCLE_PROJECT_USERNAME"} {
		if v, ok := os.LookupEnv(name); ok {
			env[name] = v
		}
	}
	r := runner.New(j.t, binaryPath, runner.WithSandboxEnv(env), runner.WithCmdDir(j.wd))
	_, err := r.Run(args...)
	out := r.StdoutString() + r.StderrString()
	assert.NilError(j.t, err, out)
	return out
}

// dotnet runs the dotnet a later step would resolve from PATH.
func (j *job) dotnet(args ...string) string {
	j.t.Helper()
	bin, err := lookPath("dotnet", j.path)
	assert.NilError(j.t, err)
	cmd := pathlink.Command(bin, args...)
	cmd.Dir = j.wd
	cmd.Env = append(os.Environ(), "PATH="+j.path, "DOTNET_CLI_TELEMETRY_OPTOUT=1")
	out, err := cmd.CombinedOutput()
	assert.NilError(j.t, err, string(out))
	return string(out)
}

// lookPath finds name on path the way a later step's shell would.
func lookPath(name, path string) (string, error) {
	dirs := pathlink.SearchDirs(path)
	i := pathlink.IndexOf(dirs, name)
	if i < 0 {
		return "", fmt.Errorf("%s is not on PATH", name)
	}
	for _, ext := range []string{"", ".exe", ".bat", ".cmd"} {
		p := filepath.Join(dirs[i], name+ext)
		if info, err := os.Stat(p); err == nil && !info.IsDir() && (ext != "" || runtime.GOOS != "windows") {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s is not on PATH", name)
}

func TestAcceptance_InstallsAnSDKAndARuntime(t *testing.T) {
	j := newJob(t)
	out := j.run("--dotnet-version", "10.0.x", "--runtime-version", "8.0", "--no-nuget-cache")
	assert.Assert(t, strings.Contains(out, ".NET SDK 10.0."), out)

	sdks := j.dotnet("--list-sdks")
	assert.Assert(t, strings.Contains(sdks, "10.0."), sdks)
	runtimes := j.dotnet("--list-runtimes")
	assert.Assert(t, strings.Contains(runtimes, "Microsoft.NETCore.App 8.0."), runtimes)
	assert.Assert(t, strings.Contains(runtimes, "Microsoft.AspNetCore.App 8.0."), runtimes)

	env, err := os.ReadFile(j.sandbox.BashEnv)
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(env), "export DOTNET_ROOT="), string(env))
}

func TestAcceptance_RestoresAndTestsASolution(t *testing.T) {
	j := newJob(t)
	copyDir(t, sample(t), j.wd)

	out := j.run()
	assert.Assert(t, strings.Contains(out, "is the one dotnet uses"), out)
	assert.Assert(t, strings.HasPrefix(strings.TrimSpace(j.dotnet("--version")), "10.0."))

	out = j.run("restore")
	assert.Assert(t, strings.Contains(out, "--locked-mode"), out)

	j.run("test", "--hang-timeout", "5m")
	var suites []string
	entries, err := os.ReadDir(filepath.Join(j.wd, "test-results"))
	assert.NilError(t, err)
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(j.wd, "test-results", e.Name()))
		assert.NilError(t, err)
		suites = append(suites, string(b))
	}
	all := strings.Join(suites, "\n")
	assert.Assert(t, strings.Contains(all, `name="Greeter.Tests" tests="4" failures="0" skipped="1"`), all)
	assert.Assert(t, strings.Contains(all, `name="Greeter.MSTests" tests="1" failures="0" skipped="0"`), all)
}

// sample finds the sample solution from the package directory, as go test
// runs it, or from the repository root, as the smoke jobs do.
func sample(t *testing.T) string {
	t.Helper()
	for _, dir := range []string{filepath.Join("testdata", "sample"), filepath.Join("acceptance", "testdata", "sample")} {
		if _, err := os.Stat(filepath.Join(dir, "Sample.slnx")); err == nil {
			return dir
		}
	}
	t.Fatal("acceptance/testdata/sample not found")
	return ""
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	assert.NilError(t, os.CopyFS(dst, os.DirFS(src)))
}
