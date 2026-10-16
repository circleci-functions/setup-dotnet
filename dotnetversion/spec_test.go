package dotnetversion_test

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/setup-dotnet/dotnetversion"
)

func TestParseSpec(t *testing.T) {
	tests := []struct {
		in    string
		kind  dotnetversion.Kind
		match []string
		miss  []string
	}{
		{in: "8.0.425", kind: dotnetversion.Exact, match: []string{"8.0.425"}, miss: []string{"8.0.426", "8.0.425-rc.1"}},
		{in: "10.0.100-rc.2.25502.107", kind: dotnetversion.Exact, match: []string{"10.0.100-rc.2.25502.107"}, miss: []string{"10.0.100"}},
		{in: "8.0", kind: dotnetversion.Channel, match: []string{"8.0.100", "8.0.426"}, miss: []string{"9.0.100"}},
		{in: "8.0.x", kind: dotnetversion.Channel, match: []string{"8.0.426"}},
		{in: "8.0.X", kind: dotnetversion.Channel, match: []string{"8.0.426"}},
		{in: "8.0.*", kind: dotnetversion.Channel, match: []string{"8.0.426"}},
		{in: "8", kind: dotnetversion.Major, match: []string{"8.0.426", "8.1.100"}, miss: []string{"9.0.100"}},
		{in: "8.x", kind: dotnetversion.Major, match: []string{"8.0.426"}},
		{in: "8.0.4xx", kind: dotnetversion.Band, match: []string{"8.0.400", "8.0.426"}, miss: []string{"8.0.399", "8.0.500"}},
		{in: "latest", kind: dotnetversion.Latest, match: []string{"8.0.100", "11.0.100-rc.1"}},
		{in: " LATEST ", kind: dotnetversion.Latest},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			spec, err := dotnetversion.ParseSpec(tc.in)
			assert.NilError(t, err)
			assert.Equal(t, spec.Kind, tc.kind)
			for _, v := range tc.match {
				assert.Assert(t, spec.Matches(mustParse(t, v)), v)
			}
			for _, v := range tc.miss {
				assert.Assert(t, !spec.Matches(mustParse(t, v)), v)
			}
		})
	}
}

func TestParseSpec_Invalid(t *testing.T) {
	for _, in := range []string{"", "lts", "8.0.4XX", "8.0.4x", "v8.0.100", "8.0.100.1", "8..1", "x"} {
		t.Run(in, func(t *testing.T) {
			_, err := dotnetversion.ParseSpec(in)
			assert.ErrorContains(t, err, "invalid version")
		})
	}
}

func TestParseSpecs(t *testing.T) {
	specs, err := dotnetversion.ParseSpecs("8.0.x, 9.0\n10.0.1xx\r\n")
	assert.NilError(t, err)
	var got []string
	for _, s := range specs {
		got = append(got, s.String())
	}
	assert.DeepEqual(t, got, []string{"8.0.x", "9.0", "10.0.1xx"})
}

func TestCompare(t *testing.T) {
	ordered := []string{
		"8.0.100-preview.1", "8.0.100-preview.7.23376.3", "8.0.100-rc.1.23455.8", "8.0.100-rc.2.23502.2",
		"8.0.100", "8.0.101", "8.0.200", "9.0.100-rc.1", "10.0.100",
	}
	for i := 1; i < len(ordered); i++ {
		a, b := mustParse(t, ordered[i-1]), mustParse(t, ordered[i])
		assert.Equal(t, a.Compare(b), -1, "%s < %s", a, b)
		assert.Equal(t, b.Compare(a), 1, "%s > %s", b, a)
	}
	assert.Equal(t, mustParse(t, "8.0.100").Compare(mustParse(t, "8.0.100")), 0)
}

func mustParse(t *testing.T, s string) dotnetversion.Version {
	t.Helper()
	v, err := dotnetversion.Parse(s)
	assert.NilError(t, err)
	return v
}
