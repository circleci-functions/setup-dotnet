package dotnetdist_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-dotnet/dotnetdist"
	"github.com/circleci-functions/setup-dotnet/dotnetversion"
)

type server struct {
	*httptest.Server
	files    map[string][]byte
	requests atomic.Int32
}

func newServer(t *testing.T) *server {
	t.Helper()
	s := &server{files: map[string][]byte{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.requests.Add(1)
		b, ok := s.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(s.Close)
	return s
}

// add serves an archive holding files and returns the artifact for it.
func (s *server) add(t *testing.T, name string, comp dotnetversion.Component, version, host string, files map[string]string) dotnetversion.Artifact {
	t.Helper()
	b := archive(t, name, files)
	s.files["/"+name] = b
	sum := sha512.Sum512(b)
	return dotnetversion.Artifact{
		Component: comp, Version: parse(t, version), HostVersion: parse(t, host),
		URL: s.URL + "/" + name, SHA512: hex.EncodeToString(sum[:]),
	}
}

func sdkFiles(sdk, host, dotnet string) map[string]string {
	return map[string]string{
		"./dotnet":                     dotnet,
		"./LICENSE.txt":                "licence " + host,
		"./host/fxr/" + host + "/fxr":  "fxr",
		"./sdk/" + sdk + "/dotnet.dll": "sdk " + sdk,
		"./shared/Microsoft.NETCore.App/" + host + "/Microsoft.NETCore.App.deps.json": "{}",
		"./packs/Microsoft.NETCore.App.Ref/" + host + "/data/FrameworkList.xml":       host,
	}
}

func TestInstall_SideBySide(t *testing.T) {
	srv := newServer(t)
	older := srv.add(t, "dotnet-sdk-8.0.426-linux-x64.tar.gz", dotnetversion.SDK, "8.0.426", "8.0.32", sdkFiles("8.0.426", "8.0.32", "host 8"))
	newer := srv.add(t, "dotnet-sdk-10.0.402-linux-x64.tar.gz", dotnetversion.SDK, "10.0.402", "10.0.13", sdkFiles("10.0.402", "10.0.13", "host 10"))
	root := dotnetdist.Root(filepath.Join(t.TempDir(), "dotnet"))
	inst := installer(root, srv)

	for _, a := range []dotnetversion.Artifact{newer, older} {
		installed, err := inst.Install(context.Background(), a)
		assert.NilError(t, err)
		assert.Assert(t, installed)
	}

	assert.DeepEqual(t, strings(root.Versions(dotnetversion.SDK)), []string{"10.0.402", "8.0.426"})
	assert.DeepEqual(t, strings(root.Hosts()), []string{"10.0.13", "8.0.32"})
	assert.Equal(t, read(t, root.Dir(), "dotnet"), "host 10", "an older archive must not replace a newer host")
	assert.Equal(t, read(t, root.Dir(), "LICENSE.txt"), "licence 10.0.13")
	assert.Assert(t, noStaging(t, root.Dir()))
}

func TestInstall_NewerHostReplacesTopLevelFiles(t *testing.T) {
	srv := newServer(t)
	older := srv.add(t, "dotnet-sdk-8.0.426-linux-x64.tar.gz", dotnetversion.SDK, "8.0.426", "8.0.32", sdkFiles("8.0.426", "8.0.32", "host 8"))
	newer := srv.add(t, "dotnet-sdk-10.0.402-linux-x64.tar.gz", dotnetversion.SDK, "10.0.402", "10.0.13", sdkFiles("10.0.402", "10.0.13", "host 10"))
	root := dotnetdist.Root(t.TempDir())
	inst := installer(root, srv)

	for _, a := range []dotnetversion.Artifact{older, newer} {
		_, err := inst.Install(context.Background(), a)
		assert.NilError(t, err)
	}
	assert.Equal(t, read(t, root.Dir(), "dotnet"), "host 10")
}

func TestInstall_SkipsWhatTheRootHas(t *testing.T) {
	srv := newServer(t)
	a := srv.add(t, "dotnet-sdk-10.0.402-linux-x64.tar.gz", dotnetversion.SDK, "10.0.402", "10.0.13", sdkFiles("10.0.402", "10.0.13", "host"))
	root := dotnetdist.Root(t.TempDir())
	inst := installer(root, srv)

	_, err := inst.Install(context.Background(), a)
	assert.NilError(t, err)
	installed, err := inst.Install(context.Background(), a)
	assert.NilError(t, err)
	assert.Assert(t, !installed)
	assert.Equal(t, srv.requests.Load(), int32(1))
}

func TestInstall_Runtime(t *testing.T) {
	srv := newServer(t)
	a := srv.add(t, "aspnetcore-runtime-8.0.32-win-x64.zip", dotnetversion.ASPNETCoreRuntime, "8.0.32", "8.0.32", map[string]string{
		"dotnet.exe":                  "host",
		"host/fxr/8.0.32/hostfxr.dll": "fxr",
		"shared/Microsoft.NETCore.App/8.0.32/Microsoft.NETCore.App.deps.json":       "{}",
		"shared/Microsoft.AspNetCore.App/8.0.32/Microsoft.AspNetCore.App.deps.json": "{}",
	})
	root := dotnetdist.Root(t.TempDir())

	_, err := installer(root, srv).Install(context.Background(), a)
	assert.NilError(t, err)
	assert.Assert(t, root.Has(dotnetversion.ASPNETCoreRuntime, parse(t, "8.0.32")))
	assert.Assert(t, !root.Has(dotnetversion.SDK, parse(t, "8.0.32")))
}

func TestInstall_Failures(t *testing.T) {
	srv := newServer(t)
	good := srv.add(t, "dotnet-sdk-10.0.402-linux-x64.tar.gz", dotnetversion.SDK, "10.0.402", "10.0.13", sdkFiles("10.0.402", "10.0.13", "host"))

	missing := good
	missing.URL = srv.URL + "/dotnet-sdk-10.0.403-linux-x64.tar.gz"
	badSum := good
	badSum.SHA512 = hex.EncodeToString(make([]byte, 64))
	escape := srv.add(t, "escape.tar.gz", dotnetversion.SDK, "10.0.402", "10.0.13", map[string]string{"../outside": "x"})
	wrong := srv.add(t, "wrong.tar.gz", dotnetversion.SDK, "9.0.100", "9.0.0", sdkFiles("10.0.402", "10.0.13", "host"))

	tests := []struct {
		name  string
		a     dotnetversion.Artifact
		want  string
		is404 bool
	}{
		{name: "404", a: missing, want: "not found", is404: true},
		{name: "checksum", a: badSum, want: "SHA-512 is"},
		{name: "path traversal", a: escape, want: "illegal path in archive"},
		{name: "archive without the version", a: wrong, want: "did not install .NET SDK 9.0.100"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := dotnetdist.Root(t.TempDir())
			_, err := installer(root, srv).Install(context.Background(), tc.a)
			assert.ErrorContains(t, err, tc.want)
			assert.Equal(t, errors.Is(err, dotnetdist.ErrNotFound), tc.is404)
			assert.Equal(t, len(root.Versions(dotnetversion.SDK)) > 0, tc.name == "archive without the version")
		})
	}
}

func TestInstall_ArchiveCache(t *testing.T) {
	srv := newServer(t)
	a := srv.add(t, "dotnet-sdk-10.0.402-linux-x64.tar.gz", dotnetversion.SDK, "10.0.402", "10.0.13", sdkFiles("10.0.402", "10.0.13", "host"))
	cache := &memCache{files: map[string]cached{}}
	archives := filepath.Join(t.TempDir(), "archives")

	for i := range 2 {
		assert.NilError(t, os.RemoveAll(archives))
		root := dotnetdist.Root(t.TempDir())
		inst := installer(root, srv)
		inst.CacheDir = archives
		inst.Cache = cache
		_, err := inst.Install(context.Background(), a)
		assert.NilError(t, err, "run %d", i)
		_, err = os.Stat(filepath.Join(inst.CacheDir, a.FileName()))
		assert.Assert(t, os.IsNotExist(err), "the archive is removed once installed")
	}
	assert.Equal(t, srv.requests.Load(), int32(1), "the second job restores the archive instead of downloading it")
	assert.DeepEqual(t, cache.saved, []string{"setup-dotnet-archive-v1-dotnet-sdk-10.0.402-linux-x64.tar.gz"})
}

func TestPlatformRID(t *testing.T) {
	tests := []struct {
		p    dotnetdist.Platform
		want string
		err  string
	}{
		{p: dotnetdist.Platform{OS: "linux", Arch: "x64"}, want: "linux-x64"},
		{p: dotnetdist.Platform{OS: "linux", Arch: "arm64", Musl: true}, want: "linux-musl-arm64"},
		{p: dotnetdist.Platform{OS: "linux", Arch: "arm"}, want: "linux-arm"},
		{p: dotnetdist.Platform{OS: "darwin", Arch: "arm64"}, want: "osx-arm64"},
		{p: dotnetdist.Platform{OS: "windows", Arch: "x86"}, want: "win-x86"},
		{p: dotnetdist.Platform{OS: "windows", Arch: "arm64"}, want: "win-arm64"},
		{p: dotnetdist.Platform{OS: "linux", Arch: "x86"}, err: "no build for linux/x86"},
		{p: dotnetdist.Platform{OS: "darwin", Arch: "x86"}, err: "no build for darwin/x86"},
		{p: dotnetdist.Platform{OS: "freebsd", Arch: "x64"}, err: "no build for freebsd/x64"},
	}
	for _, tc := range tests {
		t.Run(tc.want+tc.err, func(t *testing.T) {
			got, err := tc.p.RID()
			if tc.err != "" {
				assert.ErrorContains(t, err, tc.err)
				return
			}
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}

func TestParseArch(t *testing.T) {
	for in, want := range map[string]string{"x64": "x64", "AMD64": "x64", "x86": "x86", "arm64": "arm64", "arm": "arm"} {
		got, err := dotnetdist.ParseArch(in)
		assert.NilError(t, err)
		assert.Equal(t, got, want)
	}
	_, err := dotnetdist.ParseArch("s390x")
	assert.ErrorContains(t, err, `unsupported architecture "s390x"`)
}

// memCache keeps saved files in memory and restores them to the path they
// were saved from, as the job cache does.
type memCache struct {
	files map[string]cached
	saved []string
}

type cached struct {
	path string
	data []byte
}

func (c *memCache) Restore(_ context.Context, key string) {
	f, ok := c.files[key]
	if !ok {
		return
	}
	_ = os.MkdirAll(filepath.Dir(f.path), 0o750)
	_ = os.WriteFile(f.path, f.data, 0o600)
}

func (c *memCache) Save(_ context.Context, key string, paths []string) {
	b, _ := os.ReadFile(paths[0])
	c.files[key] = cached{path: paths[0], data: b}
	c.saved = append(c.saved, key)
}

func installer(root dotnetdist.Root, srv *server) *dotnetdist.Installer {
	return &dotnetdist.Installer{Root: root, CacheDir: filepath.Join(root.Dir() + "-archives"), HTTP: srv.Client(), Log: io.Discard}
}

func archive(t *testing.T, name string, files map[string]string) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	if filepath.Ext(name) == ".zip" {
		zw := zip.NewWriter(&buf)
		for _, n := range names {
			w, err := zw.Create(n)
			assert.NilError(t, err)
			_, err = w.Write([]byte(files[n]))
			assert.NilError(t, err)
		}
		assert.NilError(t, zw.Close())
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	assert.NilError(t, tw.WriteHeader(&tar.Header{Name: "./", Mode: 0o755, Typeflag: tar.TypeDir}))
	for _, n := range names {
		assert.NilError(t, tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(files[n])), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(files[n]))
		assert.NilError(t, err)
	}
	assert.NilError(t, tw.Close())
	assert.NilError(t, gz.Close())
	return buf.Bytes()
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	assert.NilError(t, err)
	return string(b)
}

func noStaging(t *testing.T, dir string) bool {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".setup-dotnet-*"))
	assert.NilError(t, err)
	return len(matches) == 0
}

func parse(t *testing.T, s string) dotnetversion.Version {
	t.Helper()
	v, err := dotnetversion.Parse(s)
	assert.NilError(t, err)
	return v
}

func strings(vs []dotnetversion.Version) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.String())
	}
	return out
}
