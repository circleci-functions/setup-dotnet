package clitest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func trxFixture(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "trx", "testdata", "xunit.trx"))
	assert.NilError(t, err)
	return p
}

func junitFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	assert.NilError(t, err)
	out := map[string]string{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		assert.NilError(t, err)
		out[e.Name()] = string(b)
	}
	return out
}

func TestTest_Arguments(t *testing.T) {
	tests := []struct {
		name       string
		globalJSON string
		args       []string
		want       string
	}{
		{name: "vstest", args: []string{"--project", "tests/A.Tests/A.Tests.csproj"},
			want: "dotnet test tests/A.Tests/A.Tests.csproj --logger trx;LogFilePrefix=A.Tests --results-directory"},
		{name: "filter and hang timeout", args: []string{"--filter", "Category!=Slow", "--hang-timeout", "9m"},
			want: "--filter Category!=Slow --blame-hang-timeout 9m"},
		{name: "testing platform", globalJSON: `{"test":{"runner":"Microsoft.Testing.Platform"}}`, args: []string{"--hang-timeout", "90s"},
			want: "--report-trx --results-directory"},
		{name: "testing platform hang dump", globalJSON: `{"test":{"runner":"Microsoft.Testing.Platform"}}`, args: []string{"--hang-timeout", "90s"},
			want: "--hangdump --hangdump-timeout 90s"},
		{name: "extra args last", args: []string{"--extra-args=-c Release --no-build"}, want: "-c Release --no-build"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j := newJob(t)
			j.putToolOnPath("dotnet")
			if tc.globalJSON != "" {
				j.write("global.json", tc.globalJSON)
			}
			out, err := j.run(nil, append([]string{"test"}, tc.args...)...)
			assert.NilError(t, err, out)
			calls := j.calls()
			assert.Equal(t, len(calls), 1, "%v", calls)
			assert.Assert(t, strings.Contains(calls[0], tc.want), calls[0])
		})
	}
}

func TestTest_WritesJUnitAndFailsLikeDotnet(t *testing.T) {
	j := newJob(t)
	j.putToolOnPath("dotnet")
	env := map[string]string{"FAKEDOTNET_TRX": trxFixture(t), "FAKEDOTNET_TEST_EXIT": "1"}

	out, err := j.run(env, "test", "--project", "tests/A.Tests/A.Tests.csproj")
	assert.ErrorContains(t, err, "", out)
	assert.Assert(t, strings.Contains(out, "dotnet test failed: tests/A.Tests/A.Tests.csproj"), out)
	assert.Assert(t, strings.Contains(out, "5 tests, 1 failed, 1 skipped"), out)

	files := junitFiles(t, filepath.Join(j.wd, "test-results"))
	assert.Equal(t, len(files), 1, "%v", files)
	for _, xml := range files {
		assert.Assert(t, strings.Contains(xml, `<testsuite name="Sample.xunit.Tests" tests="5" failures="1" skipped="1"`), xml)
		assert.Assert(t, strings.Contains(xml, `file="tests/A.Tests/A.Tests.csproj"`), xml)
	}
}

func TestTest_ResultsDir(t *testing.T) {
	j := newJob(t)
	j.putToolOnPath("dotnet")
	out, err := j.run(map[string]string{"FAKEDOTNET_TRX": trxFixture(t)}, "test", "--results-dir", "out/junit")
	assert.NilError(t, err, out)
	assert.Equal(t, len(junitFiles(t, filepath.Join(j.wd, "out", "junit"))), 1)
}

func TestTest_Split(t *testing.T) {
	projects := "src\\App\\App.csproj,tests\\A.Tests\\A.Tests.csproj,tests\\B.Tests\\B.Tests.csproj,tests\\C.Tests\\C.Tests.csproj"
	tests := []struct {
		index string
		want  []string
	}{
		{index: "0", want: []string{"dotnet test tests/A.Tests/A.Tests.csproj", "dotnet test tests/C.Tests/C.Tests.csproj"}},
		{index: "1", want: []string{"dotnet test tests/B.Tests/B.Tests.csproj"}},
	}
	for _, tc := range tests {
		t.Run("node "+tc.index, func(t *testing.T) {
			j := newJob(t)
			j.putToolOnPath("dotnet")
			j.putToolOnPath("circleci")
			j.write("App.slnx", "<Solution />")
			env := map[string]string{
				"CIRCLE_NODE_TOTAL": "2", "CIRCLE_NODE_INDEX": tc.index,
				"FAKEDOTNET_SLN_PROJECTS": projects, "FAKEDOTNET_TRX": trxFixture(t),
			}
			out, err := j.run(env, "test")
			assert.NilError(t, err, out)

			var ran []string
			for _, c := range j.calls() {
				if strings.HasPrefix(c, "dotnet test ") {
					ran = append(ran, strings.Join(strings.Fields(c)[:3], " "))
				}
			}
			assert.DeepEqual(t, ran, tc.want)
			assert.Assert(t, strings.Contains(strings.Join(j.calls(), "\n"), "circleci tests split --split-by=timings --timings-type=filename"))
			assert.Equal(t, len(junitFiles(t, filepath.Join(j.wd, "test-results"))), len(tc.want))
		})
	}
}

func TestTest_SplitWithNothingForThisNode(t *testing.T) {
	j := newJob(t)
	j.putToolOnPath("dotnet")
	j.putToolOnPath("circleci")
	j.write("A.Tests.csproj", "<Project />")
	env := map[string]string{"CIRCLE_NODE_TOTAL": "2", "CIRCLE_NODE_INDEX": "1"}

	out, err := j.run(env, "test")
	assert.NilError(t, err, out)
	assert.Assert(t, strings.Contains(out, "no test project falls to this node"), out)
}

func TestTest_NoSplitRunsEverything(t *testing.T) {
	j := newJob(t)
	j.putToolOnPath("dotnet")
	out, err := j.run(map[string]string{"CIRCLE_NODE_TOTAL": "4"}, "test", "--no-split")
	assert.NilError(t, err, out)
	assert.DeepEqual(t, j.calls()[0][:len("dotnet test --logger")], "dotnet test --logger")
}

func TestTest_InvalidInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{name: "hang timeout", args: []string{"--hang-timeout", "soon"}, want: `--hang-timeout "soon"`},
		{name: "two solutions", env: map[string]string{"CIRCLE_NODE_TOTAL": "2"}, want: "set --project to the one to test"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			j := newJob(t)
			j.putToolOnPath("dotnet")
			j.write("A.sln", "")
			j.write("B.sln", "")
			out, err := j.run(tc.env, append([]string{"test"}, tc.args...)...)
			assert.ErrorContains(t, err, "", out)
			assert.Assert(t, strings.Contains(out, tc.want), out)
		})
	}
}
