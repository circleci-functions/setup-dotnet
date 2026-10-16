package dotnetversion_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-dotnet/dotnetversion"
)

func metadataServer(t *testing.T) *dotnetversion.Metadata {
	t.Helper()
	srv := httptest.NewServer(http.FileServer(http.Dir("testdata/metadata")))
	t.Cleanup(srv.Close)
	return dotnetversion.NewMetadata(srv.URL, srv.Client())
}

func TestResolve(t *testing.T) {
	tests := []struct {
		name      string
		spec      string
		component dotnetversion.Component
		quality   string
		channel   string
		rid       string
		ext       string
		want      []string
	}{
		{name: "exact", spec: "8.0.425", want: []string{"8.0.425"}},
		{name: "exact prerelease", spec: "10.0.100-rc.2.25502.107", want: []string{"10.0.100-rc.2.25502.107"}},
		{name: "channel", spec: "10.0", want: []string{"10.0.402", "10.0.401", "10.0.400", "10.0.303", "10.0.113", "10.0.112", "10.0.111", "10.0.100-rc.2.25502.107"}},
		{name: "channel x", spec: "9.0.x", want: []string{"9.0.319", "9.0.318", "9.0.317", "9.0.122", "9.0.121", "9.0.120", "9.0.100-rc.2.24474.11"}},
		{name: "channel ga", spec: "9.0.x", quality: "ga", want: []string{"9.0.319", "9.0.318", "9.0.317", "9.0.122", "9.0.121", "9.0.120"}},
		{name: "prerelease channel", spec: "11.0.x", want: []string{"11.0.100-rc.2.26504.105", "11.0.100-rc.1.26425.128", "11.0.100-preview.7.26381.103"}},
		{name: "major", spec: "8.x", want: []string{"8.0.426", "8.0.425", "8.0.424", "8.0.132", "8.0.131", "8.0.130", "8.0.100-rc.2.23502.2"}},
		{name: "band", spec: "10.0.1xx", want: []string{"10.0.113", "10.0.112", "10.0.111", "10.0.100-rc.2.25502.107"}},
		{name: "latest is the newest GA", spec: "latest", want: []string{"10.0.402", "10.0.401", "10.0.400", "10.0.303", "10.0.113", "10.0.112", "10.0.111"}},
		{name: "latest preview", spec: "latest", quality: "preview", want: []string{"11.0.100-rc.2.26504.105", "11.0.100-rc.1.26425.128", "11.0.100-preview.7.26381.103"}},
		{name: "latest STS", spec: "latest", channel: "sts", want: []string{"9.0.319", "9.0.318", "9.0.317", "9.0.122", "9.0.121", "9.0.120"}},
		{name: "latest LTS", spec: "latest", channel: "LTS", want: []string{"10.0.402", "10.0.401", "10.0.400", "10.0.303", "10.0.113", "10.0.112", "10.0.111"}},
		{name: "latest band channel", spec: "latest", channel: "8.0.4xx", want: []string{"8.0.426", "8.0.425", "8.0.424"}},
		{name: "a named channel may be EOL", spec: "latest", channel: "7.0.4xx", want: []string{"7.0.410", "7.0.409", "7.0.408"}},
		{name: "aspnetcore runtime", spec: "8.0", component: dotnetversion.ASPNETCoreRuntime, want: []string{"8.0.32", "8.0.31", "8.0.30", "8.0.0-rc.2.23480.2"}},
		{name: "windows desktop runtime", spec: "10.0.13", component: dotnetversion.WindowsDesktopRuntime, rid: "win-x64", ext: ".zip", want: []string{"10.0.13"}},
		{name: "musl", spec: "10.0.402", rid: "linux-musl-x64", ext: ".tar.gz", want: []string{"10.0.402"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := dotnetversion.ParseSpec(tc.spec)
			assert.NilError(t, err)
			rid, ext := tc.rid, tc.ext
			if rid == "" {
				rid, ext = "linux-x64", ".tar.gz"
			}
			got, err := metadataServer(t).Resolve(context.Background(), dotnetversion.Query{
				Spec: spec, Component: tc.component, Quality: tc.quality, Channel: tc.channel, RID: rid, Ext: ext,
			})
			assert.NilError(t, err)
			var versions []string
			for _, a := range got {
				versions = append(versions, a.Version.String())
				assert.Assert(t, a.URL != "" && len(a.SHA512) == 128, "%+v", a)
			}
			assert.DeepEqual(t, versions, tc.want)
		})
	}
}

func TestResolve_Artifact(t *testing.T) {
	spec, err := dotnetversion.ParseSpec("10.0.402")
	assert.NilError(t, err)
	got, err := metadataServer(t).Resolve(context.Background(), dotnetversion.Query{Spec: spec, RID: "win-x64", Ext: ".zip"})
	assert.NilError(t, err)
	assert.Equal(t, len(got), 1)
	assert.Equal(t, got[0].FileName(), "dotnet-sdk-10.0.402-win-x64.zip")
	assert.Equal(t, got[0].HostVersion.String(), "10.0.13")
}

func TestResolve_Errors(t *testing.T) {
	tests := []struct {
		name      string
		spec      string
		component dotnetversion.Component
		channel   string
		rid       string
		want      string
	}{
		{name: "unpublished version", spec: "10.0.499", want: `no .NET SDK matches "10.0.499" for linux-x64`},
		{name: "unknown channel", spec: "12.0", want: `no .NET release channel matches "12.0"`},
		{name: "band for a runtime", spec: "8.0.4xx", component: dotnetversion.ASPNETCoreRuntime, want: "feature bands are for SDKs"},
		{name: "bad channel", spec: "latest", channel: "current", want: `invalid dotnet-channel "current"`},
		{name: "no build for the rid", spec: "8.0.425", rid: "linux-riscv64", want: "for linux-riscv64"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := dotnetversion.ParseSpec(tc.spec)
			assert.NilError(t, err)
			rid := tc.rid
			if rid == "" {
				rid = "linux-x64"
			}
			_, err = metadataServer(t).Resolve(context.Background(), dotnetversion.Query{
				Spec: spec, Component: tc.component, Channel: tc.channel, RID: rid, Ext: ".tar.gz",
			})
			assert.ErrorContains(t, err, tc.want)
		})
	}
}
