package clitest_test

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestRestore(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		args  []string
		want  []string
	}{
		{name: "plain", files: []string{"App.csproj"}, want: []string{"dotnet restore"}},
		{name: "locked mode with a lock file", files: []string{"src/App/packages.lock.json"}, args: []string{"--project", "src/App/App.csproj"},
			want: []string{"dotnet restore src/App/App.csproj --locked-mode"}},
		{name: "locked mode off", files: []string{"packages.lock.json"}, args: []string{"--no-locked-mode"}, want: []string{"dotnet restore"}},
		{name: "tools", files: []string{".config/dotnet-tools.json"}, want: []string{"dotnet restore", "dotnet tool restore"}},
		{name: "tools off", files: []string{".config/dotnet-tools.json"}, args: []string{"--no-tool-restore"}, want: []string{"dotnet restore"}},
		{name: "extra args", args: []string{`--extra-args=-r linux-x64 -p:"Name=A B"`}, want: []string{"dotnet restore -r linux-x64 -p:Name=A B"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j := newJob(t)
			j.putToolOnPath("dotnet")
			for _, f := range tc.files {
				j.write(f, "{}")
			}
			out, err := j.run(nil, append([]string{"restore"}, tc.args...)...)
			assert.NilError(t, err, out)
			assert.DeepEqual(t, j.calls(), tc.want)
		})
	}
}

func TestRestore_NeedsDotnet(t *testing.T) {
	out, err := newJob(t).run(nil, "restore")
	assert.ErrorContains(t, err, "", out)
	assert.Assert(t, strings.Contains(out, "no dotnet on PATH: run setup-dotnet first"), out)
}

func TestRestore_BadExtraArgs(t *testing.T) {
	j := newJob(t)
	j.putToolOnPath("dotnet")
	out, err := j.run(nil, "restore", `--extra-args=-p:"unterminated`)
	assert.ErrorContains(t, err, "", out)
	assert.Assert(t, strings.Contains(out, "unterminated double quote"), out)
}
