package nuget_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/golden"

	"github.com/circleci-functions/setup-dotnet/nuget"
)

func tree(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		assert.NilError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		assert.NilError(t, os.WriteFile(p, []byte(f), 0o600))
	}
	return dir
}

func TestDependencyFiles(t *testing.T) {
	tests := []struct {
		name     string
		files    []string
		patterns []string
		want     []string
	}{
		{
			name:  "lock files win",
			files: []string{"App.sln", "src/App/App.csproj", "src/App/packages.lock.json", "tests/T/packages.lock.json", ".config/dotnet-tools.json", "src/App/obj/packages.lock.json"},
			want:  []string{".config/dotnet-tools.json", "src/App/packages.lock.json", "tests/T/packages.lock.json"},
		},
		{
			name:  "project files without a lock file",
			files: []string{"Directory.Packages.props", "NuGet.Config", "global.json", "src/App/App.csproj", "src/Lib/Lib.fsproj", "src/Old/packages.config", "src/App/bin/Debug/x.csproj", "README.md", "node_modules/x/x.csproj"},
			want:  []string{"Directory.Packages.props", "NuGet.Config", "global.json", "src/App/App.csproj", "src/Lib/Lib.fsproj", "src/Old/packages.config"},
		},
		{
			name:     "patterns replace the search",
			files:    []string{"a/packages.lock.json", "b/packages.lock.json", "b/B.csproj"},
			patterns: []string{"b/**/*.json", "**/B.csproj", "b/packages.lock.json"},
			want:     []string{"b/B.csproj", "b/packages.lock.json"},
		},
		{name: "nothing", files: []string{"README.md"}, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := nuget.DependencyFiles(tree(t, tc.files...), tc.patterns)
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tc.want)
		})
	}
}

func TestHash(t *testing.T) {
	dir := tree(t, "a/packages.lock.json", "b/packages.lock.json")
	h1, err := nuget.Hash(dir, []string{"a/packages.lock.json", "b/packages.lock.json"})
	assert.NilError(t, err)
	h2, err := nuget.Hash(dir, []string{"a/packages.lock.json"})
	assert.NilError(t, err)
	assert.Assert(t, h1 != h2)

	assert.NilError(t, os.WriteFile(filepath.Join(dir, "a", "packages.lock.json"), []byte("changed"), 0o600))
	h3, err := nuget.Hash(dir, []string{"a/packages.lock.json", "b/packages.lock.json"})
	assert.NilError(t, err)
	assert.Assert(t, h1 != h3)
}

func TestKeys(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	dir := filepath.Join(string(filepath.Separator)+"home", "u", ".nuget", "packages")
	save, restore := nuget.Keys("linux-amd64", dir, "abc", now)
	assert.Assert(t, strings.HasPrefix(save, "setup-dotnet-nuget-v1-linux-amd64-"), save)
	assert.Assert(t, strings.HasSuffix(save, "-1759968000-abc"), save)
	assert.DeepEqual(t, restore, []string{save, strings.TrimSuffix(save, "abc")})

	later, _ := nuget.Keys("linux-amd64", dir, "abc", now.Add(72*time.Hour))
	assert.Assert(t, later != save, "keys rotate")
	other, _ := nuget.Keys("linux-amd64", filepath.Join(string(filepath.Separator)+"root", ".nuget", "packages"), "abc", now)
	assert.Assert(t, other != save, "a cache at another path gets another key, since it restores to the path it was saved from")
}

func TestGlobalPackagesDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	got, err := nuget.GlobalPackagesDir(exec.Command("sh", "-c", "echo 'info : x'; echo 'global-packages: /home/u/.nuget/packages/'"))
	assert.NilError(t, err)
	assert.Equal(t, got, filepath.Clean("/home/u/.nuget/packages"))

	_, err = nuget.GlobalPackagesDir(exec.Command("sh", "-c", "echo boom >&2; exit 3"))
	assert.ErrorContains(t, err, "boom")
}

func TestAuthenticate(t *testing.T) {
	src := nuget.Source{URL: "https://nuget.pkg.github.com/acme/index.json", Username: "acme", Token: "s3cr3t"}
	existing := filepath.Join("testdata", "nuget.config")
	tests := []struct {
		name     string
		existing string
		golden   string
	}{
		{name: "new source", golden: "auth-new-source.config"},
		{name: "existing config without the source", existing: filepath.Join("testdata", "missing.config"), golden: "auth-new-source.config"},
		{name: "credentials for the matching keys", existing: existing, golden: "auth-matching-keys.config"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p, err := nuget.Authenticate(dir, tc.existing, src)
			assert.NilError(t, err)
			assert.Equal(t, p, filepath.Join(dir, nuget.ConfigName))
			b, err := os.ReadFile(p)
			assert.NilError(t, err)
			golden.Assert(t, string(b), tc.golden)
			if runtime.GOOS != "windows" {
				info, err := os.Stat(p)
				assert.NilError(t, err)
				assert.Equal(t, info.Mode().Perm(), os.FileMode(0o600))
			}
		})
	}
}

func TestAuthenticate_InvalidConfig(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "nuget.config")
	assert.NilError(t, os.WriteFile(bad, []byte("<settings/>"), 0o600))
	_, err := nuget.Authenticate(t.TempDir(), bad, nuget.Source{URL: "https://x"})
	assert.ErrorContains(t, err, "is not a valid NuGet.config")
}

func TestEncodeKey(t *testing.T) {
	for in, want := range map[string]string{
		"github":     "github",
		"my feed":    "my_x0020_feed",
		"1st-feed.x": "_x0031_st-feed.x",
		"a/b":        "a_x002F_b",
	} {
		assert.Equal(t, nuget.EncodeKey(in), want)
	}
}

func TestFindConfig(t *testing.T) {
	dir := tree(t, "NuGet.Config")
	assert.Equal(t, nuget.FindConfig(dir), filepath.Join(dir, "NuGet.Config"))
	assert.Equal(t, nuget.FindConfig(t.TempDir()), "")
}
