package dotnetpath_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-dotnet/dotnetpath"
)

// fakeRoot makes a .NET root whose dotnet lists a runtime under itself, as
// the real host does.
func fakeRoot(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake dotnet is a shell script")
	}
	root := t.TempDir()
	script := "#!/bin/sh\nroot=$(cd \"$(dirname \"$(readlink -f \"$0\")\")\" && pwd)\n" +
		"echo \"Microsoft.NETCore.App 10.0.13 [$root/shared/Microsoft.NETCore.App]\"\n"
	assert.NilError(t, os.WriteFile(filepath.Join(root, "dotnet"), []byte(script), 0o755)) //nolint:gosec // a test executable
	return root
}

func TestLink(t *testing.T) {
	root := fakeRoot(t)
	assert.NilError(t, os.WriteFile(filepath.Join(root, "dnx"), []byte("#!/bin/sh\n"), 0o755)) //nolint:gosec // a test executable
	bin := t.TempDir()

	res, err := dotnetpath.Link(dotnetpath.Options{Root: root, Path: bin})
	assert.NilError(t, err)
	assert.Equal(t, res.Dir, bin)
	for _, name := range []string{"dotnet", "dnx"} {
		target, err := os.Readlink(filepath.Join(bin, name))
		assert.NilError(t, err)
		assert.Equal(t, target, filepath.Join(root, name))
	}
}

func TestLink_ReplacesAnotherLink(t *testing.T) {
	other, root := fakeRoot(t), fakeRoot(t)
	ahead, behind := t.TempDir(), t.TempDir()
	assert.NilError(t, os.Symlink(filepath.Join(other, "dotnet"), filepath.Join(behind, "dotnet")))

	res, err := dotnetpath.Link(dotnetpath.Options{Root: root, Path: ahead + string(os.PathListSeparator) + behind})
	assert.NilError(t, err)
	assert.Equal(t, res.Dir, behind)
	target, err := os.Readlink(filepath.Join(behind, "dotnet"))
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(root, "dotnet"))
	_, err = os.Stat(filepath.Join(other, "dotnet"))
	assert.NilError(t, err, "the other installation is left alone")
}

func TestVerify(t *testing.T) {
	root, other := fakeRoot(t), fakeRoot(t)
	assert.NilError(t, dotnetpath.Verify(root)(filepath.Join(root, "dotnet")))
	assert.ErrorContains(t, dotnetpath.Verify(root)(filepath.Join(other, "dotnet")), "runs the .NET at")
}

func TestExport(t *testing.T) {
	bashEnv := filepath.Join(t.TempDir(), "bash_env")
	t.Setenv("BASH_ENV", bashEnv)

	assert.NilError(t, dotnetpath.Export(map[string]string{"DOTNET_ROOT": "/home/it's/.dotnet"}, []string{"/home/u/.dotnet/tools"}))
	b, err := os.ReadFile(bashEnv)
	assert.NilError(t, err)
	got := string(b)
	assert.Assert(t, strings.Contains(got, `export DOTNET_ROOT='/home/it'\''s/.dotnet'`+"\n"), got)
	assert.Assert(t, strings.Contains(got, `export PATH='/home/u/.dotnet/tools':"$PATH"`+"\n"), got)
}

func TestExport_NoBashEnv(t *testing.T) {
	t.Setenv("BASH_ENV", "")
	assert.ErrorContains(t, dotnetpath.Export(map[string]string{"A": "b"}, nil), "BASH_ENV is not set")
}

func TestToolsDir(t *testing.T) {
	t.Setenv("DOTNET_CLI_HOME", "/cli")
	got, err := dotnetpath.ToolsDir()
	assert.NilError(t, err)
	assert.Equal(t, got, filepath.Join("/cli", ".dotnet", "tools"))
}

func TestRegister(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows registers .NET in the registry")
	}
	dir := filepath.Join(t.TempDir(), "etc", "dotnet")
	for _, root := range []string{"/home/u/.dotnet", "/opt/dotnet"} {
		p, err := dotnetpath.Register(dir, root, "x64")
		assert.NilError(t, err)
		assert.Equal(t, p, filepath.Join(dir, "install_location_x64"))
		b, err := os.ReadFile(p)
		assert.NilError(t, err)
		assert.Equal(t, string(b), root+"\n")
	}
}
