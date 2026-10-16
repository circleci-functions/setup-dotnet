//go:build acceptance

package setupdotnet_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/skip"

	"github.com/circleci-functions/toolkit/sdk"
	"github.com/circleci-functions/toolkit/sdk/sdktest"
	"github.com/circleci-functions/toolkit/testing/runner"
)

// TestAcceptance_InstallsOntoTheRealPath is the one test that lets
// setup-dotnet choose where .NET goes and where dotnet is linked. That choice
// depends on the executor, and a sandboxed suite never reaches it.
//
// It changes the machine it runs on, up to installing next to a .NET already
// there, so it runs only where that is disposable: a CircleCI job, or a
// container the caller vouches for with $SETUP_DOTNET_DISPOSABLE (see
// `task smoke-local`).
func TestAcceptance_InstallsOntoTheRealPath(t *testing.T) {
	if os.Getenv("SETUP_DOTNET_DISPOSABLE") == "" {
		sdktest.SkipUnlessInJob(t)
	}

	s := sdktest.NewSandbox(t, sdktest.WithLiveCache())
	bashEnv := filepath.Join(t.TempDir(), "bash_env")
	wd := t.TempDir()
	before := rootOnPath()
	install := func(t *testing.T) string {
		t.Helper()
		r := runner.New(t, binaryPath,
			runner.WithUnsafeOSEnvVars(),
			runner.WithUnsafeAdditionalEnvVars(map[string]string{"BASH_ENV": bashEnv, "DOTNET_CLI_TELEMETRY_OPTOUT": "1"}),
			runner.WithCmdDir(wd),
		)
		_, err := r.Run("--dotnet-version", acceptanceVersion(), "--cache-nonce", s.CacheKeyNonce)
		out := r.StdoutString() + r.StderrString()
		assert.NilError(t, err, "got: %s", out)
		return out
	}

	out := install(t)
	t.Logf("setup-dotnet: %s", out)
	version := reported(t, out, `\.NET SDK (\S+) (?:installed in|is already in) `)
	root := reported(t, out, `\.NET SDK \S+ (?:installed in|is already in) (.+)$`)
	resolved := find(out, `dotnet resolves from (.+)$`)
	shadowed := find(out, `the dotnet\.exe in (.+) comes first on PATH`)
	if resolved != "" {
		t.Logf("setup-dotnet installed .NET SDK %s into %s, and dotnet resolves from %s", version, root, resolved)
		if !samePath(resolved, root) {
			removeLinks(t, resolved)
		}
	} else {
		assert.Assert(t, runtime.GOOS == "windows" && shadowed != "", "setup-dotnet neither linked dotnet nor reported why: %s", out)
		t.Logf("setup-dotnet installed .NET SDK %s and put it first on PATH in bash, ahead of the dotnet in %s", version, shadowed)
	}

	laterStep := func(t *testing.T, name string, args ...string) (string, error) {
		t.Helper()
		if shadowed == "" {
			return sdktest.LaterStep(t, name, args...)
		}
		return sdktest.LaterStep(t, "sh", append([]string{"-c", `. "$1" && shift && exec "$@"`, "sh", bashEnv, name}, args...)...)
	}

	t.Run("ResolvesByNameInALaterStep", func(t *testing.T) {
		got, err := laterStep(t, "dotnet", "--list-sdks")
		assert.NilError(t, err, "a later step could not resolve dotnet: %s", got)
		assert.Assert(t, listsFrom(got, version, root), "a later step runs another dotnet: %s", got)
	})

	if runtime.GOOS == "windows" {
		t.Run("ResolvesInPowerShellToo", func(t *testing.T) {
			skip.If(t, shadowed != "", "PowerShell does not read $BASH_ENV, so it keeps the dotnet in %s", shadowed)
			got, err := sdktest.LaterStep(t, "powershell.exe", "-Command", `dotnet --list-sdks; exit $LASTEXITCODE`)
			assert.NilError(t, err, "PowerShell could not resolve dotnet: %s", got)
			assert.Assert(t, listsFrom(got, version, root), "PowerShell resolved a different dotnet: %s", got)
		})
	}

	t.Run("DotnetRootReachesALaterShellStep", func(t *testing.T) {
		got, err := sdktest.LaterStep(t, "sh", "-c", `. "$1" && "$DOTNET_ROOT/dotnet" --list-sdks`, "sh", bashEnv)
		assert.NilError(t, err, "DOTNET_ROOT does not lead to a .NET that runs: %s", got)
		assert.Assert(t, listsFrom(got, version, root), "DOTNET_ROOT points at another .NET: %s", got)
	})

	t.Run("AnAppHostRunsInALaterStep", func(t *testing.T) {
		app := t.TempDir()
		got, err := laterStep(t, "dotnet", "new", "console", "--name", "Hello", "--output", app)
		assert.NilError(t, err, got)
		bin := filepath.Join(app, "out")
		got, err = laterStep(t, "dotnet", "build", app, "--output", bin)
		assert.NilError(t, err, got)
		apphost := filepath.Join(bin, "Hello")
		if runtime.GOOS == "windows" {
			apphost += ".exe"
		}

		got, err = sdktest.LaterStep(t, "sh", "-c", `. "$1" && "$2"`, "sh", bashEnv, filepath.ToSlash(apphost))
		assert.NilError(t, err, "the apphost does not find .NET from a bash step: %s", got)
		assert.Assert(t, strings.Contains(got, "Hello, World!"), got)

		skip.If(t, shadowed != "", "outside bash, an apphost finds the .NET in %s", shadowed)
		got, err = sdktest.LaterStep(t, apphost)
		assert.NilError(t, err, "the apphost does not find .NET from a step that sources nothing: %s", got)
		assert.Assert(t, strings.Contains(got, "Hello, World!"), got)
	})

	t.Run("LeavesOtherInstallationsAlone", func(t *testing.T) {
		skip.If(t, before == "" || samePath(before, root), "no other .NET was on PATH")
		got, err := sdktest.LaterStep(t, filepath.Join(before, exe("dotnet")), "--list-sdks")
		assert.NilError(t, err, "the .NET in %s no longer runs: %s", before, got)
		assert.Assert(t, !listsFrom(got, version, root), "the dotnet in %s now runs the .NET setup-dotnet installed: %s", before, got)
	})

	t.Run("RestoresFromTheCacheOnAFreshExecutor", func(t *testing.T) {
		skip.If(t, !sdk.InCircleCIJob(), "the cache round trip needs a CircleCI job's cache")
		skip.If(t, samePath(before, root), "setup-dotnet installed next to the executor's own .NET, which is not ours to remove")
		assert.NilError(t, os.RemoveAll(root))
		out := install(t)
		assert.Assert(t, !strings.Contains(out, "downloading"), "expected the archive saved above to be restored; got: %s", out)
		got, err := laterStep(t, "dotnet", "--list-sdks")
		assert.NilError(t, err, got)
		assert.Assert(t, listsFrom(got, version, root), got)
	})
}

// rootOnPath returns the root of the dotnet on PATH before the test, or "".
func rootOnPath() string {
	bin, err := exec.LookPath("dotnet")
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(bin); err == nil {
		bin = real
	}
	return filepath.Dir(bin)
}

// listsFrom reports whether dotnet --list-sdks output lists version from root.
func listsFrom(out, version, root string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		v, dir, ok := strings.Cut(strings.TrimSpace(line), " [")
		if ok && v == version && samePath(filepath.Dir(strings.TrimSuffix(dir, "]")), root) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return strings.EqualFold(resolve(a), resolve(b))
}

// removeLinks takes dotnet back off PATH at the end of the test.
func removeLinks(t *testing.T, dir string) {
	t.Helper()
	t.Cleanup(func() {
		for _, name := range []string{"dotnet", "dnx"} {
			for _, ext := range []string{"", ".bat"} {
				_ = os.Remove(filepath.Join(dir, name+ext))
			}
		}
	})
}

// reported returns the first submatch of pattern in setup-dotnet's output.
func reported(t *testing.T, out, pattern string) string {
	t.Helper()
	if got := find(out, pattern); got != "" {
		return got
	}
	t.Fatalf("setup-dotnet did not report %q:\n%s", pattern, out)
	return ""
}

// find returns the first submatch of pattern in out, or "".
func find(out, pattern string) string {
	re := regexp.MustCompile(pattern)
	for line := range strings.SplitSeq(out, "\n") {
		if m := re.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			return m[1]
		}
	}
	return ""
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
