// Package clitest_test runs the setup-dotnet binary end to end against fakes:
// a release server whose archives hold a fake dotnet, and the sandbox's
// in-memory job cache. Hermetic, so it runs under task test.
package clitest_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/sdktest"
	"github.com/circleci-functions/toolkit/testing/compiler"
	"github.com/circleci-functions/toolkit/testing/runner"
)

var binaryPath, fakeDotnetPath string

func TestMain(m *testing.M) {
	status, err := runTests(m)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(status)
}

func runTests(m *testing.M) (int, error) {
	p := compiler.NewParallel(2)
	defer p.Cleanup()
	p.Add(compiler.Work{Result: &binaryPath, Name: "setup-dotnet", Target: "..", Source: "."})
	p.Add(compiler.Work{Result: &fakeDotnetPath, Name: "fakedotnet", Target: "../internal/testing/fakedotnet", Source: "."})
	if err := p.Run(context.Background()); err != nil {
		return 0, err
	}
	return m.Run(), nil
}

// job is one sandboxed run environment: a home directory, a PATH holding only
// a bin directory, and a job cache.
type job struct {
	t       *testing.T
	sandbox *sdktest.Sandbox
	wd      string
	log     string
	env     map[string]string
}

func newJob(t *testing.T) *job {
	t.Helper()
	s := sdktest.NewSandbox(t)
	j := &job{t: t, sandbox: s, wd: t.TempDir(), log: filepath.Join(t.TempDir(), "calls.log")}
	path := s.BinDir
	if runtime.GOOS == "windows" {
		path += string(os.PathListSeparator) + filepath.Join(os.Getenv("SystemRoot"), "System32")
	}
	j.env = map[string]string{
		"PATH":           path,
		"FAKEDOTNET_LOG": j.log,
		"USERPROFILE":    s.Home,
		"LOCALAPPDATA":   filepath.Join(s.Home, "AppData", "Local"),
		"APPDATA":        filepath.Join(s.Home, "AppData", "Roaming"),
	}
	return j
}

// run runs setup-dotnet in the job's working directory.
func (j *job) run(extra map[string]string, args ...string) (string, error) {
	j.t.Helper()
	env := j.sandbox.Env()
	maps.Copy(env, j.env)
	maps.Copy(env, extra)
	r := runner.New(j.t, binaryPath, runner.WithSandboxEnv(env), runner.WithCmdDir(j.wd))
	_, err := r.Run(args...)
	return r.StdoutString() + r.StderrString(), err
}

func (j *job) root() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(j.sandbox.Home, "AppData", "Local", "Microsoft", "dotnet")
	}
	return filepath.Join(j.sandbox.Home, ".dotnet")
}

// calls returns what the fake dotnet and circleci were run with.
func (j *job) calls() []string {
	b, err := os.ReadFile(j.log)
	if os.IsNotExist(err) {
		return nil
	}
	assert.NilError(j.t, err)
	var out []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		call, _, _ := strings.Cut(line, "\t")
		out = append(out, call)
	}
	return out
}

func (j *job) write(name, content string) {
	j.t.Helper()
	p := filepath.Join(j.wd, filepath.FromSlash(name))
	assert.NilError(j.t, os.MkdirAll(filepath.Dir(p), 0o750))
	assert.NilError(j.t, os.WriteFile(p, []byte(content), 0o600))
}

func (j *job) bashEnv() string {
	b, err := os.ReadFile(j.sandbox.BashEnv)
	if os.IsNotExist(err) {
		return ""
	}
	assert.NilError(j.t, err)
	return string(b)
}

// putDotnetOnPath installs the fake dotnet as the job's own .NET, outside the
// bin directory, with the given SDKs.
func (j *job) putDotnetOnPath(sdks ...string) string {
	j.t.Helper()
	root := filepath.Join(j.t.TempDir(), "dotnet")
	files := map[string]string{"shared/Microsoft.NETCore.App/10.0.13/Microsoft.NETCore.App.deps.json": "{}"}
	for _, v := range sdks {
		files["sdk/"+v+"/dotnet.dll"] = v
	}
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		assert.NilError(j.t, os.MkdirAll(filepath.Dir(p), 0o750))
		assert.NilError(j.t, os.WriteFile(p, []byte(content), 0o600))
	}
	copyExe(j.t, fakeDotnetPath, filepath.Join(root, exe("dotnet")))
	j.env["PATH"] = root + string(os.PathListSeparator) + j.env["PATH"]
	real, err := filepath.EvalSymlinks(root)
	assert.NilError(j.t, err)
	return real
}

// putToolOnPath puts the fake dotnet into the bin directory under name.
func (j *job) putToolOnPath(name string) {
	j.t.Helper()
	copyExe(j.t, fakeDotnetPath, filepath.Join(j.sandbox.BinDir, exe(name)))
}

func copyExe(t *testing.T, src, dst string) {
	t.Helper()
	b, err := os.ReadFile(src)
	assert.NilError(t, err)
	assert.NilError(t, os.MkdirAll(filepath.Dir(dst), 0o750))
	assert.NilError(t, os.WriteFile(dst, b, 0o755)) //nolint:gosec // a test executable
}

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// releases is a fake builds.dotnet.microsoft.com: release metadata, and
// archives holding the fake dotnet.
type releases struct {
	*httptest.Server
	mu       sync.Mutex
	files    map[string][]byte
	requests map[string]int
	index    []map[string]string
	channels map[string][]map[string]any
}

func newReleases(t *testing.T) *releases {
	t.Helper()
	r := &releases{files: map[string][]byte{}, requests: map[string]int{}, channels: map[string][]map[string]any{}}
	r.Server = httptest.NewServer(http.HandlerFunc(r.serve))
	t.Cleanup(r.Close)
	return r
}

func (r *releases) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.requests[req.URL.Path]++
	b, ok := r.files[req.URL.Path]
	r.mu.Unlock()
	if !ok {
		http.NotFound(w, req)
		return
	}
	_, _ = w.Write(b)
}

func (r *releases) metadataURL() string {
	return r.URL + "/release-metadata"
}

// downloads counts requests for archives, as opposed to metadata.
func (r *releases) downloads() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for p, c := range r.requests {
		if strings.HasPrefix(p, "/dotnet/") {
			n += c
		}
	}
	return n
}

func (r *releases) metadataRequests() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for p, c := range r.requests {
		if strings.HasPrefix(p, "/release-metadata/") {
			n += c
		}
	}
	return n
}

// channel adds a release channel to the index.
func (r *releases) channel(version, phase, kind string) {
	r.index = append(r.index, map[string]string{"channel-version": version, "support-phase": phase, "release-type": kind})
}

// release publishes a release of channel: its SDKs, and an ASP.NET Core and
// Windows Desktop runtime of version runtime. Archives named in missing are
// listed but not served.
func (r *releases) release(t *testing.T, channel, runtimeVersion string, sdks []string, missing ...string) {
	t.Helper()
	rid, ext := platformRID(), ".tar.gz"
	if runtime.GOOS == "windows" {
		ext = ".zip"
	}
	product := func(prefix, version string, files map[string]string) map[string]any {
		name := fmt.Sprintf("%s-%s-%s%s", prefix, version, rid, ext)
		url := r.URL + "/dotnet/" + name
		b := archive(t, ext, files)
		sum := sha512.Sum512(b)
		if !slices.Contains(missing, version) {
			r.files["/dotnet/"+name] = b
		}
		return map[string]any{"version": version, "files": []map[string]string{{
			"name": prefix + "-" + rid + ext, "rid": rid, "url": url, "hash": hex.EncodeToString(sum[:]),
		}}}
	}
	host := map[string]string{
		exe("dotnet"):                         string(must(os.ReadFile(fakeDotnetPath))),
		"host/fxr/" + runtimeVersion + "/fxr": "fxr",
		"shared/Microsoft.NETCore.App/" + runtimeVersion + "/Microsoft.NETCore.App.deps.json": "{}",
	}
	with := func(extra map[string]string) map[string]string {
		out := maps.Clone(host)
		maps.Copy(out, extra)
		return out
	}
	var sdkProducts []map[string]any
	for _, v := range sdks {
		sdkProducts = append(sdkProducts, product("dotnet-sdk", v, with(map[string]string{"sdk/" + v + "/dotnet.dll": v})))
	}
	rel := map[string]any{
		"release-version": runtimeVersion,
		"runtime":         map[string]any{"version": runtimeVersion},
		"sdks":            sdkProducts,
		"aspnetcore-runtime": product("aspnetcore-runtime", runtimeVersion, with(map[string]string{
			"shared/Microsoft.AspNetCore.App/" + runtimeVersion + "/Microsoft.AspNetCore.App.deps.json": "{}",
		})),
		"windowsdesktop": product("windowsdesktop-runtime", runtimeVersion, with(map[string]string{
			"shared/Microsoft.WindowsDesktop.App/" + runtimeVersion + "/Microsoft.WindowsDesktop.App.deps.json": "{}",
		})),
	}
	r.channels[channel] = append(r.channels[channel], rel)
	r.publish(t)
}

func (r *releases) publish(t *testing.T) {
	t.Helper()
	r.files["/release-metadata/releases-index.json"] = must(json.Marshal(map[string]any{"releases-index": r.index}))
	for ch, rels := range r.channels {
		r.files["/release-metadata/"+ch+"/releases.json"] = must(json.Marshal(map[string]any{"releases": rels}))
	}
}

// standardReleases publishes 10.0 (LTS, current), 9.0 (STS) and 8.0 (LTS).
func standardReleases(t *testing.T, missing ...string) *releases {
	t.Helper()
	r := newReleases(t)
	r.channel("10.0", "active", "lts")
	r.channel("9.0", "maintenance", "sts")
	r.channel("8.0", "maintenance", "lts")
	r.release(t, "10.0", "10.0.13", []string{"10.0.402"}, missing...)
	r.release(t, "10.0", "10.0.12", []string{"10.0.401"}, missing...)
	r.release(t, "9.0", "9.0.21", []string{"9.0.319"}, missing...)
	r.release(t, "8.0", "8.0.32", []string{"8.0.426"}, missing...)
	return r
}

func platformRID() string {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	switch runtime.GOOS {
	case "darwin":
		return "osx-" + arch
	case "windows":
		return "win-" + arch
	}
	matches, _ := filepath.Glob("/lib/ld-musl-*.so.1")
	if len(matches) > 0 {
		return "linux-musl-" + arch
	}
	return "linux-" + arch
}

func archive(t *testing.T, ext string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if ext == ".zip" {
		zw := zip.NewWriter(&buf)
		for name, content := range files {
			w, err := zw.Create(name)
			assert.NilError(t, err)
			_, err = w.Write([]byte(content))
			assert.NilError(t, err)
		}
		assert.NilError(t, zw.Close())
		return buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		assert.NilError(t, tw.WriteHeader(&tar.Header{Name: "./" + name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(content))
		assert.NilError(t, err)
	}
	assert.NilError(t, tw.Close())
	assert.NilError(t, gz.Close())
	return buf.Bytes()
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
