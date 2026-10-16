// Command fakedotnet stands in for the dotnet host in tests. It answers the
// queries setup-dotnet makes from the .NET root it sits in, records every
// call in $FAKEDOTNET_LOG, and writes $FAKEDOTNET_TRX as dotnet test's
// results. Installed as circleci, it splits tests by node index.
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func main() {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fail(err)
	}
	record()
	if strings.TrimSuffix(filepath.Base(exe), ".exe") == "circleci" {
		split()
		return
	}
	os.Exit(dotnet(filepath.Dir(exe), os.Args[1:]))
}

func dotnet(root string, args []string) int {
	switch {
	case has(args, "--list-runtimes"):
		listRuntimes(root)
	case has(args, "--list-sdks"):
		for _, v := range dirs(filepath.Join(root, "sdk")) {
			fmt.Printf("%s [%s]\n", v, filepath.Join(root, "sdk"))
		}
	case has(args, "--version"):
		version(root)
	case len(args) > 0 && args[0] == "test":
		return test(args)
	default:
		project(args)
	}
	return 0
}

// project answers the queries about a project and its packages.
func project(args []string) {
	switch {
	case len(args) > 1 && args[0] == "nuget" && args[1] == "locals":
		dir := os.Getenv("NUGET_PACKAGES")
		if dir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".nuget", "packages") + string(filepath.Separator)
		}
		fmt.Println("global-packages: " + dir)
	case len(args) > 2 && args[0] == "sln" && args[2] == "list":
		fmt.Print("Project(s)\n----------\n")
		for p := range strings.SplitSeq(os.Getenv("FAKEDOTNET_SLN_PROJECTS"), ",") {
			fmt.Println(p)
		}
	case len(args) > 1 && args[0] == "msbuild":
		fmt.Println(strconv.FormatBool(strings.Contains(args[1], "Tests")))
	}
}

func listRuntimes(root string) {
	shared := filepath.Join(root, "shared")
	for _, fw := range dirs(shared) {
		for _, v := range dirs(filepath.Join(shared, fw)) {
			fmt.Printf("%s %s [%s]\n", fw, v, filepath.Join(shared, fw))
		}
	}
}

func version(root string) {
	if v := os.Getenv("FAKEDOTNET_VERSION"); v != "" {
		fmt.Println(v)
		return
	}
	sdks := dirs(filepath.Join(root, "sdk"))
	slices.SortFunc(sdks, compareVersions)
	if len(sdks) == 0 {
		fmt.Fprintln(os.Stderr, "No .NET SDKs were found.")
		os.Exit(1)
	}
	fmt.Println(sdks[len(sdks)-1])
}

func test(args []string) int {
	i := slices.Index(args, "--results-directory")
	if src := os.Getenv("FAKEDOTNET_TRX"); src != "" && i >= 0 && i+1 < len(args) {
		b, err := os.ReadFile(src) //nolint:gosec // a test fixture
		if err != nil {
			fail(err)
		}
		name := "results"
		if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
			name = strings.TrimSuffix(filepath.Base(args[1]), filepath.Ext(args[1]))
		}
		if err := os.MkdirAll(args[i+1], 0o750); err != nil { //nolint:gosec // the directory the caller names

			fail(err)
		}
		if err := os.WriteFile(filepath.Join(args[i+1], name+".trx"), b, 0o600); err != nil { //nolint:gosec // the directory the caller names
			fail(err)
		}
	}
	code, _ := strconv.Atoi(os.Getenv("FAKEDOTNET_TEST_EXIT"))
	return code
}

// split prints the stdin lines whose position, modulo the node count, is
// this node's index.
func split() {
	index, _ := strconv.Atoi(os.Getenv("CIRCLE_NODE_INDEX"))
	total, _ := strconv.Atoi(os.Getenv("CIRCLE_NODE_TOTAL"))
	sc := bufio.NewScanner(os.Stdin)
	for n := 0; sc.Scan(); n++ {
		if total > 0 && n%total == index {
			fmt.Println(sc.Text())
		}
	}
	if err := sc.Err(); err != nil {
		fail(err)
	}
}

func record() {
	log := os.Getenv("FAKEDOTNET_LOG")
	if log == "" {
		return
	}
	f, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // a test log
	if err != nil {
		fail(err)
	}
	defer f.Close()
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	wd, _ := os.Getwd()
	fmt.Fprintf(f, "%s %s\t%s\n", name, strings.Join(os.Args[1:], " "), wd)
}

func dirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// compareVersions orders release versions numerically, part by part.
func compareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		x, _ := strconv.Atoi(as[i])
		y, _ := strconv.Atoi(bs[i])
		if x != y {
			return x - y
		}
	}
	return len(as) - len(bs)
}

func has(args []string, flag string) bool {
	return len(args) > 0 && args[0] == flag
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "fakedotnet:", err)
	os.Exit(70)
}
