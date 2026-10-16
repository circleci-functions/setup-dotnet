package globaljson_test

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-dotnet/dotnetversion"
	"github.com/circleci-functions/setup-dotnet/globaljson"
)

var published = []string{
	"8.0.100-rc.2.23502.2", "8.0.100", "8.0.101", "8.0.110", "8.0.200", "8.0.204", "8.0.300", "8.0.426",
	"9.0.100", "9.0.319", "10.0.100-rc.2.25502.107", "10.0.100", "10.0.402",
}

func TestSelect(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		wantBest string
		want     []string
	}{
		{name: "patch prefers the exact version", json: `{"sdk":{"version":"8.0.101"}}`, want: []string{"8.0.101", "8.0.110"}},
		{name: "patch rolls to the band's latest", json: `{"sdk":{"version":"8.0.102"}}`, want: []string{"8.0.110"}},
		{name: "feature takes the nearest band", json: `{"sdk":{"version":"8.0.150","rollForward":"feature"}}`, want: []string{"8.0.204", "8.0.200", "8.0.300", "8.0.426"}},
		{name: "minor", json: `{"sdk":{"version":"8.0.400","rollForward":"minor"}}`, want: []string{"8.0.426"}},
		{name: "major moves to the next major", json: `{"sdk":{"version":"8.0.500","rollForward":"major"}}`, want: []string{"9.0.100", "9.0.319", "10.0.100", "10.0.402"}},
		{name: "latestPatch", json: `{"sdk":{"version":"8.0.200","rollForward":"latestPatch"}}`, want: []string{"8.0.204", "8.0.200"}},
		{name: "latestFeature", json: `{"sdk":{"version":"8.0.200","rollForward":"latestFeature"}}`, want: []string{"8.0.426", "8.0.300", "8.0.204", "8.0.200"}},
		{name: "latestMinor", json: `{"sdk":{"version":"8.0.400","rollForward":"latestMinor"}}`, want: []string{"8.0.426"}},
		{name: "latestMajor", json: `{"sdk":{"version":"9.0.100","rollForward":"latestMajor"}}`, want: []string{"10.0.402", "10.0.100", "9.0.319", "9.0.100"}},
		{name: "no version is latestMajor", json: `{"sdk":{}}`, wantBest: "10.0.402"},
		{name: "disable needs an exact match", json: `{"sdk":{"version":"8.0.110","rollForward":"disable"}}`, want: []string{"8.0.110"}},
		{name: "a prerelease is met exactly", json: `{"sdk":{"version":"8.0.100-rc.2.23502.2","rollForward":"latestMajor"}}`, want: []string{"8.0.100-rc.2.23502.2"}},
		{name: "allowPrerelease", json: `{"sdk":{"version":"10.0.100-rc.1.0","rollForward":"latestFeature","allowPrerelease":true}}`, want: nil},
		{name: "allowPrerelease with a release", json: `{"sdk":{"version":"9.0.300","rollForward":"latestMajor","allowPrerelease":true}}`, want: []string{"10.0.402", "10.0.100", "10.0.100-rc.2.25502.107", "9.0.319"}},
		{name: "case-insensitive policy", json: `{"sdk":{"version":"8.0.200","rollForward":"LATESTPATCH"}}`, want: []string{"8.0.204", "8.0.200"}},
		{name: "comments and trailing commas", json: "// pin\n{\"sdk\": {\"version\": \"8.0.101\", /* why */ \"rollForward\": \"disable\",},}", want: []string{"8.0.101"}},
		{name: "nothing matches", json: `{"sdk":{"version":"7.0.100","rollForward":"disable"}}`, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := load(t, tc.json).Constraint()
			assert.NilError(t, err)
			got := strings(c.Select(versions(t, published)))
			if tc.wantBest != "" {
				assert.Assert(t, len(got) > 0)
				assert.Equal(t, got[0], tc.wantBest)
				return
			}
			assert.DeepEqual(t, got, tc.want)
		})
	}
}

func TestConstraint_Errors(t *testing.T) {
	tests := []struct {
		json, want string
	}{
		{json: `{"sdk":{"version":"8.0"}}`, want: `sdk.version "8.0" must be a full SDK version`},
		{json: `{"sdk":{"version":"8.0.100","rollForward":"sideways"}}`, want: `unknown sdk.rollForward "sideways"`},
		{json: `{"sdk":{"rollForward":"patch"}}`, want: `sdk.rollForward "patch" needs an sdk.version`},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			_, err := load(t, tc.json).Constraint()
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestChannels(t *testing.T) {
	tests := []struct {
		json         string
		major, minor int
		want         bool
	}{
		{json: `{"sdk":{"version":"8.0.100"}}`, major: 8, minor: 0, want: true},
		{json: `{"sdk":{"version":"8.0.100"}}`, major: 9, minor: 0, want: false},
		{json: `{"sdk":{"version":"8.0.100","rollForward":"latestMajor"}}`, major: 10, minor: 0, want: true},
		{json: `{"sdk":{"version":"8.0.100","rollForward":"major"}}`, major: 7, minor: 0, want: false},
		{json: `{"sdk":{}}`, major: 6, minor: 0, want: true},
	}
	for _, tc := range tests {
		c, err := load(t, tc.json).Constraint()
		assert.NilError(t, err)
		assert.Equal(t, c.Channels(tc.major, tc.minor), tc.want, "%s %d.%d", tc.json, tc.major, tc.minor)
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "src", "app")
	assert.NilError(t, os.MkdirAll(deep, 0o750))

	got, err := globaljson.Find(deep)
	assert.NilError(t, err)
	assert.Equal(t, got, "")

	want := filepath.Join(root, "global.json")
	assert.NilError(t, os.WriteFile(want, []byte(`{}`), 0o600))
	got, err = globaljson.Find(deep)
	assert.NilError(t, err)
	assert.Equal(t, got, want)
}

func TestTestRunner(t *testing.T) {
	assert.Assert(t, load(t, `{"test":{"runner":"Microsoft.Testing.Platform"}}`).UsesTestingPlatform())
	assert.Assert(t, !load(t, `{"sdk":{"version":"10.0.100"}}`).UsesTestingPlatform())
}

func TestStripJSONC(t *testing.T) {
	tests := []struct{ in, want string }{
		{in: `{"a": "// not a comment", "b": "/* nor this */"}`, want: `{"a": "// not a comment", "b": "/* nor this */"}`},
		{in: "{\"a\": 1, // trailing\n}", want: "{\"a\": 1 \n}"},
		{in: `{"a": [1, 2,], "b": "x\"y",}`, want: `{"a": [1, 2], "b": "x\"y"}`},
		{in: "\xef\xbb\xbf{}", want: "{}"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			assert.Equal(t, string(globaljson.StripJSONC([]byte(tc.in))), tc.want)
		})
	}
}

func load(t *testing.T, content string) *globaljson.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "global.json")
	assert.NilError(t, os.WriteFile(p, []byte(content), 0o600))
	f, err := globaljson.Load(p)
	assert.NilError(t, err)
	return f
}

func versions(t *testing.T, in []string) []dotnetversion.Version {
	t.Helper()
	var out []dotnetversion.Version
	for _, s := range in {
		v, err := dotnetversion.Parse(s)
		assert.NilError(t, err)
		out = append(out, v)
	}
	return out
}

func strings(in []dotnetversion.Version) []string {
	var out []string
	for _, v := range in {
		out = append(out, v.String())
	}
	return out
}
