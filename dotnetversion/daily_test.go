package dotnetversion_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-dotnet/dotnetversion"
)

func TestDaily(t *testing.T) {
	const build = "11.0.100-rtm.26514.118"
	sum := strings.Repeat("AB", 64)
	mux := http.NewServeMux()
	mux.HandleFunc("/aka/11.0.1xx/daily/dotnet-sdk-linux-x64.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/public/Sdk/"+build+"/dotnet-sdk-11.0.100-linux-x64.tar.gz", http.StatusFound)
	})
	mux.HandleFunc("/aka/11.0.1xx/daily/dotnet-sdk-linux-x64.tar.gz.sha512", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/public-checksums/Sdk/"+build+"/dotnet-sdk-11.0.100-linux-x64.tar.gz.sha512", http.StatusFound)
	})
	mux.HandleFunc("/public/Sdk/", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/public-checksums/Sdk/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sum + "\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	a, err := dotnetversion.Daily(context.Background(), srv.Client(), srv.URL+"/aka", "11.0.1xx", dotnetversion.SDK, "linux-x64", ".tar.gz")
	assert.NilError(t, err)
	assert.Equal(t, a.Version.String(), build)
	assert.Equal(t, a.SHA512, strings.ToLower(sum))
	assert.Equal(t, a.FileName(), "dotnet-sdk-11.0.100-linux-x64.tar.gz")
}

func TestDailyChannel(t *testing.T) {
	tests := []struct {
		spec, channel, want string
	}{
		{spec: "10.0", want: "10.0"},
		{spec: "10.0.x", want: "10.0"},
		{spec: "11.0.1xx", want: "11.0.1xx"},
		{spec: "9", want: "9.0"},
		{spec: "latest", want: "11.0"},
		{spec: "latest", channel: "LTS", want: "10.0"},
		{spec: "latest", channel: "10.0.2xx", want: "10.0.2xx"},
	}
	for _, tc := range tests {
		t.Run(tc.spec+" "+tc.channel, func(t *testing.T) {
			spec, err := dotnetversion.ParseSpec(tc.spec)
			assert.NilError(t, err)
			got, err := metadataServer(t).DailyChannel(context.Background(), spec, tc.channel)
			assert.NilError(t, err)
			assert.Equal(t, got, tc.want)
		})
	}
}
