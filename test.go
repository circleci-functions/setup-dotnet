package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/circleci-functions/setup-dotnet/globaljson"
	"github.com/circleci-functions/setup-dotnet/trx"
)

type testCmd struct {
	Project     string `help:"Solution, project or directory to test. Empty lets dotnet find one in the working directory."`
	Filter      string `help:"Filter expression for dotnet test --filter, e.g. Category!=Integration."`
	ResultsDir  string `default:"test-results" help:"Directory to write JUnit XML into, for a store_test_results step."`
	Split       bool   `default:"true" negatable:"" help:"With parallelism, run only this node's share of the test projects, split by timings."`
	HangTimeout string `help:"Fail a test that runs longer than this, e.g. 9m, with a hang dump, rather than the step timing out with no output."`
	ExtraArgs   string `help:"Extra arguments for dotnet test, split as a shell would but never evaluated."`
}

var timespanRe = regexp.MustCompile(`^\d+(\.\d+)?(ms|s|m|h)?$`)

func (c *testCmd) Run() error {
	bin, err := lookDotnet()
	if err != nil {
		return fmt.Errorf("no dotnet on PATH: run setup-dotnet first")
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	extra, err := c.validate()
	if err != nil {
		return err
	}
	targets, err := c.targets(bin, wd)
	if err != nil || len(targets) == 0 {
		return err
	}

	tmp, err := os.MkdirTemp("", "setup-dotnet-trx-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	resultsDir := c.ResultsDir
	if !filepath.IsAbs(resultsDir) {
		resultsDir = filepath.Join(wd, resultsDir)
	}

	mtp := usesTestingPlatform(wd)
	var failed []string
	for i, target := range targets {
		dir := filepath.Join(tmp, strconv.Itoa(i))
		if err := runDotnet(bin, wd, c.args(target, dir, mtp, extra)...); err != nil {
			failed = append(failed, orDefault(target, "tests"))
		}
		file := ""
		if isProjectFile(target) {
			file = relSlash(wd, target)
		}
		if err := writeJUnit(dir, resultsDir, file); err != nil {
			return err
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("dotnet test failed: %s", strings.Join(failed, ", "))
	}
	return nil
}

func (c *testCmd) validate() ([]string, error) {
	if c.HangTimeout != "" && !timespanRe.MatchString(c.HangTimeout) {
		return nil, fmt.Errorf("--hang-timeout %q: use a number with ms, s, m or h, e.g. 9m", c.HangTimeout)
	}
	extra, err := shellWords(c.ExtraArgs)
	if err != nil {
		return nil, fmt.Errorf("--extra-args: %w", err)
	}
	return extra, nil
}

// targets is what to run dotnet test on: the project as given, or with
// parallelism this node's share of the test projects. An empty share means
// there is nothing to run here.
func (c *testCmd) targets(bin, wd string) ([]string, error) {
	if !c.Split || nodeTotal() < 2 {
		return []string{c.Project}, nil
	}
	targets, err := c.share(bin, wd)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		logf("no test project falls to this node")
	} else {
		logf("this node runs %s", strings.Join(targets, ", "))
	}
	return targets, nil
}

// args builds dotnet test's arguments. Microsoft.Testing.Platform takes
// its own TRX and hang-dump options in place of VSTest's.
func (c *testCmd) args(target, resultsDir string, mtp bool, extra []string) []string {
	args := []string{"test"}
	if target != "" {
		args = append(args, target)
	}
	if mtp {
		args = append(args, "--report-trx", "--results-directory", resultsDir)
	} else {
		args = append(args, "--logger", "trx;LogFilePrefix="+trxPrefix(target), "--results-directory", resultsDir)
	}
	if c.Filter != "" {
		args = append(args, "--filter", c.Filter)
	}
	if c.HangTimeout != "" {
		if mtp {
			args = append(args, "--hangdump", "--hangdump-timeout", c.HangTimeout)
		} else {
			args = append(args, "--blame-hang-timeout", c.HangTimeout)
		}
	}
	return append(args, extra...)
}

func trxPrefix(target string) string {
	name := strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
	if target == "" || name == "." || name == "" {
		return "tests"
	}
	return strings.NewReplacer(";", "_", "=", "_").Replace(name)
}

// writeJUnit converts every TRX file under dir into JUnit XML in resultsDir.
func writeJUnit(dir, resultsDir, file string) error {
	var trxFiles []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".trx") {
			trxFiles = append(trxFiles, p)
		}
		return nil
	})
	if len(trxFiles) == 0 {
		return nil
	}
	if err := os.MkdirAll(resultsDir, 0o755); err != nil { //nolint:gosec // test results are not secret
		return err
	}
	for _, p := range trxFiles {
		if err := convertOne(p, resultsDir, file); err != nil {
			return err
		}
	}
	return nil
}

func convertOne(trxPath, resultsDir, file string) error {
	in, err := os.Open(trxPath) //nolint:gosec // a file dotnet test wrote
	if err != nil {
		return err
	}
	defer in.Close()
	suite, err := trx.Convert(in, "", file)
	if err != nil {
		return fmt.Errorf("%s: %w", trxPath, err)
	}
	base := strings.TrimSuffix(filepath.Base(trxPath), filepath.Ext(trxPath))
	out := filepath.Join(resultsDir, base+".xml")
	for n := 2; exists(out); n++ {
		out = filepath.Join(resultsDir, fmt.Sprintf("%s-%d.xml", base, n))
	}
	f, err := os.Create(out) //nolint:gosec // a path under the results directory
	if err != nil {
		return err
	}
	if err := trx.Write(f, suite); err != nil {
		_ = f.Close()
		return err
	}
	logf("%d tests, %d failed, %d skipped: %s", suite.Tests, suite.Failures, suite.Skipped, relSlash(filepath.Dir(resultsDir), out))
	return f.Close()
}

// share returns this node's test projects, split by timings across the
// job's parallel nodes.
func (c *testCmd) share(bin, wd string) ([]string, error) {
	projects, err := testProjects(bin, wd, c.Project)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("found no test projects to split")
	}
	circleci, err := exec.LookPath("circleci")
	if err != nil {
		return nil, errors.New("splitting needs the circleci CLI, which every CircleCI job has; set --no-split to run every test project")
	}
	cmd := exec.Command(circleci, "tests", "split", "--split-by=timings", "--timings-type=filename") //nolint:gosec // the job's own CLI
	cmd.Dir = wd
	cmd.Stdin = strings.NewReader(strings.Join(projects, "\n") + "\n")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("circleci tests split: %w", err)
	}
	return strings.Fields(string(out)), nil
}

// testProjects lists the test projects project leads to, as slash-separated
// paths relative to wd. It restores first, since a test SDK's package is what
// marks a project as a test project.
func testProjects(bin, wd, project string) ([]string, error) {
	entry, err := entryPoint(wd, project)
	if err != nil {
		return nil, err
	}
	candidates := []string{entry}
	if isSolution(entry) {
		if candidates, err = solutionProjects(bin, wd, entry); err != nil {
			return nil, err
		}
	}
	if err := runDotnet(bin, wd, "restore", entry); err != nil {
		return nil, err
	}
	var out []string
	for _, p := range candidates {
		cmd := dotnetCmd(bin, "msbuild", p, "-getProperty:IsTestProject", "-nologo")
		cmd.Dir, cmd.Stdout = wd, nil
		res, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("evaluate %s: %w", p, err)
		}
		if strings.EqualFold(strings.TrimSpace(string(res)), "true") {
			out = append(out, p)
		}
	}
	return out, nil
}

func solutionProjects(bin, wd, solution string) ([]string, error) {
	cmd := dotnetCmd(bin, "sln", solution, "list")
	cmd.Dir, cmd.Stdout = wd, nil
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("dotnet sln list: %w", err)
	}
	slnDir := filepath.Dir(filepath.Join(wd, solution))
	var projects []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if isProjectFile(line) {
			p := filepath.Join(slnDir, filepath.FromSlash(strings.ReplaceAll(line, `\`, "/")))
			projects = append(projects, relSlash(wd, p))
		}
	}
	return projects, nil
}

// entryPoint finds the solution or project dotnet would use for project, as
// a path relative to wd.
func entryPoint(wd, project string) (string, error) {
	if isSolution(project) || isProjectFile(project) {
		if !filepath.IsAbs(project) {
			project = filepath.Join(wd, project)
		}
		return relSlash(wd, project), nil
	}
	dir := projectDir(wd, project)
	for _, match := range []func(string) bool{isSolution, isProjectFile} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", err
		}
		var found []string
		for _, e := range entries {
			if !e.IsDir() && match(e.Name()) {
				found = append(found, e.Name())
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return relSlash(wd, filepath.Join(dir, found[0])), nil
		}
		return "", fmt.Errorf("%s holds %s; set --project to the one to test", dir, strings.Join(found, " and "))
	}
	return "", fmt.Errorf("found no solution or project file in %s", dir)
}

func isSolution(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".sln", ".slnx":
		return true
	}
	return false
}

func isProjectFile(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".csproj", ".fsproj", ".vbproj":
		return true
	}
	return false
}

func usesTestingPlatform(wd string) bool {
	p, err := globaljson.Find(wd)
	if err != nil || p == "" {
		return false
	}
	f, err := globaljson.Load(p)
	return err == nil && f.UsesTestingPlatform()
}

func nodeTotal() int {
	n, _ := strconv.Atoi(os.Getenv("CIRCLE_NODE_TOTAL"))
	return n
}

func relSlash(base, p string) string {
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(p)
	}
	if rel, err := filepath.Rel(base, p); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
