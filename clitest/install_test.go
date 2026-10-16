package clitest_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func sdks(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "sdk"))
	if os.IsNotExist(err) {
		return nil
	}
	assert.NilError(t, err)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func runtimes(t *testing.T, root, framework string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "shared", framework))
	if os.IsNotExist(err) {
		return nil
	}
	assert.NilError(t, err)
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestInstall_Versions(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		globalJSON string
		wantSDKs   []string
		wantASPNET []string
	}{
		{name: "channel", args: []string{"--dotnet-version", "10.0.x"}, wantSDKs: []string{"10.0.402"}},
		{name: "exact", args: []string{"--dotnet-version", "10.0.401"}, wantSDKs: []string{"10.0.401"}},
		{name: "several", args: []string{"--dotnet-version", "8.0\n10.0.x"}, wantSDKs: []string{"10.0.402", "8.0.426"}},
		{name: "latest STS", args: []string{"--dotnet-version", "latest", "--dotnet-channel", "STS"}, wantSDKs: []string{"9.0.319"}},
		{name: "global.json", globalJSON: `{"sdk":{"version":"9.0.300","rollForward":"latestFeature"}}`, wantSDKs: []string{"9.0.319"}},
		{name: "global.json and a version", args: []string{"--dotnet-version", "10.0.x"}, globalJSON: `{"sdk":{"version":"8.0.426"}}`, wantSDKs: []string{"10.0.402", "8.0.426"}},
		{name: "nothing asked installs the latest LTS", wantSDKs: []string{"10.0.402"}},
		{name: "runtimes", args: []string{"--dotnet-version", "10.0", "--runtime-version", "8.0"}, wantSDKs: []string{"10.0.402"}, wantASPNET: []string{"8.0.32"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rel := standardReleases(t)
			j := newJob(t)
			if tc.globalJSON != "" {
				j.write("global.json", tc.globalJSON)
			}
			out, err := j.run(nil, append([]string{"--metadata-url", rel.metadataURL(), "--no-cache"}, tc.args...)...)
			assert.NilError(t, err, out)
			assert.DeepEqual(t, sdks(t, j.root()), tc.wantSDKs)
			if tc.wantASPNET != nil {
				assert.DeepEqual(t, runtimes(t, j.root(), "Microsoft.AspNetCore.App"), tc.wantASPNET)
				if runtime.GOOS == "windows" {
					assert.DeepEqual(t, runtimes(t, j.root(), "Microsoft.WindowsDesktop.App"), tc.wantASPNET)
				}
			}
		})
	}
}

func TestInstall_LinksAndExports(t *testing.T) {
	rel := standardReleases(t)
	j := newJob(t)

	out, err := j.run(nil, "--metadata-url", rel.metadataURL(), "--no-cache", "--dotnet-version", "10.0.x")
	assert.NilError(t, err, out)
	assert.Assert(t, strings.Contains(out, ".NET SDK 10.0.402 installed in "+j.root()), out)
	assert.Assert(t, strings.Contains(out, "dotnet resolves from "+j.sandbox.BinDir), out)
	assert.Assert(t, strings.Contains(out, ".NET SDK 10.0.402 is the one dotnet uses"), out)

	if runtime.GOOS != "windows" {
		loc := filepath.Join(j.sandbox.InstallDir, "etc-dotnet", "install_location_"+platformArch())
		b, err := os.ReadFile(loc)
		assert.NilError(t, err)
		assert.Equal(t, strings.TrimSpace(string(b)), j.root())
	}

	env := j.bashEnv()
	assert.Assert(t, strings.Contains(env, "export DOTNET_ROOT="), env)
	assert.Assert(t, strings.Contains(env, filepath.ToSlash(filepath.Join(j.root()))), env)
	assert.Assert(t, strings.Contains(env, "export PATH="), env)
}

func TestInstall_SecondRunDownloadsNothing(t *testing.T) {
	rel := standardReleases(t)
	j := newJob(t)
	args := []string{"--metadata-url", rel.metadataURL(), "--no-cache", "--dotnet-version", "10.0.x"}

	_, err := j.run(nil, args...)
	assert.NilError(t, err)
	out, err := j.run(nil, args...)
	assert.NilError(t, err, out)
	assert.Assert(t, strings.Contains(out, ".NET SDK 10.0.402 is already in"), out)
	assert.Equal(t, rel.downloads(), 1)
}

func TestInstall_FallsBackWhenTheNewestIsNotPublished(t *testing.T) {
	rel := standardReleases(t, "10.0.402")
	j := newJob(t)

	out, err := j.run(nil, "--metadata-url", rel.metadataURL(), "--no-cache", "--dotnet-version", "10.0.x")
	assert.NilError(t, err, out)
	assert.Assert(t, strings.Contains(out, ".NET SDK 10.0.402 is not downloadable yet, so installing .NET SDK 10.0.401"), out)
	assert.DeepEqual(t, sdks(t, j.root()), []string{"10.0.401"})
}

func TestInstall_ExactVersionDoesNotFallBack(t *testing.T) {
	rel := standardReleases(t, "10.0.402")
	j := newJob(t)

	out, err := j.run(nil, "--metadata-url", rel.metadataURL(), "--no-cache", "--dotnet-version", "10.0.402")
	assert.ErrorContains(t, err, "", out)
	assert.Assert(t, strings.Contains(out, "not found"), out)
	assert.Assert(t, sdks(t, j.root()) == nil)
}

func TestInstall_KeepsTheDotnetOnPathWhenNothingIsAsked(t *testing.T) {
	rel := standardReleases(t)
	j := newJob(t)
	root := j.putDotnetOnPath("9.0.100")

	out, err := j.run(nil, "--metadata-url", rel.metadataURL(), "--no-cache")
	assert.NilError(t, err, out)
	assert.Assert(t, strings.Contains(out, "using the .NET at "+root), out)
	assert.Equal(t, rel.downloads(), 0)
	assert.Equal(t, rel.metadataRequests(), 0)
}

func TestInstall_CheckLatestFalseReusesAMatch(t *testing.T) {
	rel := standardReleases(t)
	j := newJob(t)
	root := j.putDotnetOnPath("10.0.300")

	out, err := j.run(nil, "--metadata-url", rel.metadataURL(), "--no-cache", "--dotnet-version", "10.0.x", "--no-check-latest")
	assert.NilError(t, err, out)
	assert.Assert(t, strings.Contains(out, "using the .NET at "+root), out)
	assert.Equal(t, rel.metadataRequests(), 0)

	out, err = j.run(map[string]string{"DOTNET_CHECK_LATEST": "false"}, "--metadata-url", rel.metadataURL(), "--no-cache", "--dotnet-version", "9.0.x")
	assert.NilError(t, err, out)
	assert.DeepEqual(t, sdks(t, j.root()), []string{"9.0.319"})
}

func TestInstall_WarnsWhenANewerSDKWins(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only on Windows does setup-dotnet install next to another .NET")
	}
	rel := standardReleases(t)
	j := newJob(t)
	j.putDotnetOnPath("11.0.100")

	out, err := j.run(nil, "--metadata-url", rel.metadataURL(), "--no-cache", "--dotnet-version", "10.0.x")
	assert.NilError(t, err, out)
	assert.Assert(t, strings.Contains(out, "add a global.json to pin the SDK"), out)
}

func TestInstall_ArchiveCache(t *testing.T) {
	rel := standardReleases(t)
	j := newJob(t)
	args := []string{"--metadata-url", rel.metadataURL(), "--dotnet-version", "10.0.x", "--no-nuget-cache"}

	out, err := j.run(nil, args...)
	assert.NilError(t, err, out)
	assert.NilError(t, os.RemoveAll(j.root()))

	out, err = j.run(nil, args...)
	assert.NilError(t, err, out)
	assert.Equal(t, rel.downloads(), 1, "the second install restores the archive from the job cache")
	assert.DeepEqual(t, sdks(t, j.root()), []string{"10.0.402"})
}

func TestInstall_NuGetCacheRoundTrip(t *testing.T) {
	rel := standardReleases(t)
	j := newJob(t)
	j.write("src/App/App.csproj", "<Project />")
	packages := filepath.Join(j.sandbox.Home, ".nuget", "packages")
	args := []string{"--metadata-url", rel.metadataURL(), "--dotnet-version", "10.0.x", "--no-sdk-cache"}

	out, err := j.run(nil, args...)
	assert.NilError(t, err, out)
	pending := j.sandbox.Cache.PendingCacheSaves()
	assert.Assert(t, slices.ContainsFunc(pending, func(k string) bool { return strings.HasPrefix(k, "setup-dotnet-nuget-v1-") }), "%v", pending)

	pkg := filepath.Join(packages, "newtonsoft.json", "13.0.3", "newtonsoft.json.nuspec")
	assert.NilError(t, os.MkdirAll(filepath.Dir(pkg), 0o750))
	assert.NilError(t, os.WriteFile(pkg, []byte("<package/>"), 0o600))
	j.sandbox.Cache.EndJobAfterFailure(t)
	assert.NilError(t, os.RemoveAll(packages))

	out, err = j.run(nil, args...)
	assert.NilError(t, err, out)
	_, err = os.Stat(pkg)
	assert.NilError(t, err, "a failed job's packages come back in the next one")
}

func TestInstall_NuGetCacheNeedsProjectFiles(t *testing.T) {
	rel := standardReleases(t)
	j := newJob(t)

	out, err := j.run(nil, "--metadata-url", rel.metadataURL(), "--dotnet-version", "10.0.x", "--no-sdk-cache")
	assert.NilError(t, err, out)
	assert.Assert(t, strings.Contains(out, "NuGet cache skipped: no packages.lock.json or project file"), out)
	assert.Equal(t, len(j.sandbox.Cache.PendingCacheSaves()), 0)
}

func TestInstall_Authenticate(t *testing.T) {
	rel := standardReleases(t)
	j := newJob(t)
	args := []string{"--metadata-url", rel.metadataURL(), "--no-cache", "--dotnet-version", "10.0.x",
		"--source-url", "https://nuget.pkg.github.com/acme/index.json", "--token-env", "GH_PACKAGES"}

	out, err := j.run(nil, args...)
	assert.ErrorContains(t, err, "", out)
	assert.Assert(t, strings.Contains(out, "$GH_PACKAGES is empty"), out)

	out, err = j.run(map[string]string{"GH_PACKAGES": "s3cr3t", "CIRCLE_PROJECT_USERNAME": "acme"}, args...)
	assert.NilError(t, err, out)
	dir := filepath.Join(j.sandbox.Home, ".nuget", "NuGet", "config")
	if runtime.GOOS == "windows" {
		dir = filepath.Join(j.sandbox.Home, "AppData", "Roaming", "NuGet", "config")
	}
	b, err := os.ReadFile(filepath.Join(dir, "setup-dotnet.config"))
	assert.NilError(t, err)
	assert.Assert(t, strings.Contains(string(b), `<add key="Username" value="acme">`), string(b))
	assert.Assert(t, strings.Contains(string(b), `<add key="ClearTextPassword" value="s3cr3t">`), string(b))
	assert.Assert(t, !strings.Contains(out, "s3cr3t"), "the token is never printed")
}

func TestInstall_Workloads(t *testing.T) {
	rel := standardReleases(t)
	j := newJob(t)

	out, err := j.run(nil, "--metadata-url", rel.metadataURL(), "--no-cache", "--dotnet-version", "10.0.x", "--workloads", "wasm-tools, aspire")
	assert.NilError(t, err, out)
	calls := j.calls()
	assert.Assert(t, slices.Contains(calls, "dotnet workload update"), "%v", calls)
	assert.Assert(t, slices.Contains(calls, "dotnet workload install wasm-tools aspire"), "%v", calls)
}

func TestInstall_InstallDir(t *testing.T) {
	rel := standardReleases(t)
	j := newJob(t)
	dir := filepath.Join(t.TempDir(), "custom")

	out, err := j.run(map[string]string{"DOTNET_INSTALL_DIR": dir}, "--metadata-url", rel.metadataURL(), "--no-cache", "--dotnet-version", "8.0")
	assert.NilError(t, err, out)
	assert.DeepEqual(t, sdks(t, dir), []string{"8.0.426"})
}

func TestInstall_InvalidInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "version", args: []string{"--dotnet-version", "ten"}, want: `invalid version "ten"`},
		{name: "architecture", args: []string{"--architecture", "s390x"}, want: `unsupported architecture "s390x"`},
		{name: "quality", args: []string{"--dotnet-quality", "nightly"}, want: "--dotnet-quality"},
		{name: "global.json path", args: []string{"--global-json-file", "nope/global.json"}, want: "does not exist"},
		{name: "unpublished", args: []string{"--dotnet-version", "7.0"}, want: `no .NET release channel matches "7.0"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rel := standardReleases(t)
			out, err := newJob(t).run(nil, append([]string{"--metadata-url", rel.metadataURL(), "--no-cache"}, tc.args...)...)
			assert.ErrorContains(t, err, "", out)
			assert.Assert(t, strings.Contains(out, tc.want), out)
		})
	}
}
