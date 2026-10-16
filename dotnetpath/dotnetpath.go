// Package dotnetpath makes a .NET root's dotnet the one later steps run.
package dotnetpath

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/pathlink"
)

type Options struct {
	Root string
	// Path is the search path to link against. Empty means $PATH.
	Path string
	// BinDir links into exactly this directory. Tests set it.
	BinDir string
}

type Result struct {
	// Dir is the directory dotnet now resolves from.
	Dir string
	// Shadowed is set on Windows when another .NET's dotnet.exe comes first on
	// PATH and Link left it alone: a dotnet.exe swapped for a shim breaks the
	// installation it belongs to.
	Shadowed string
}

// Link makes dotnet resolvable by name from a directory already on PATH,
// pointing at the root's dotnet. On Unix it links dnx too, when the root has it.
func Link(opts Options) (Result, error) {
	names := []string{"dotnet"}
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(filepath.Join(opts.Root, "dnx")); err == nil {
			names = append(names, "dnx")
		}
	}
	dirs := pathlink.SearchDirs(opts.Path)
	if opts.BinDir == "" && runtime.GOOS == "windows" {
		if dir, ok := takesOver(dirs, opts.Root); ok {
			return Result{Shadowed: dir}, nil
		}
	}
	res, err := pathlink.Ensure(pathlink.Options{
		Names:     names,
		FromDir:   opts.Root,
		Preferred: pathlink.UserBinDirs(),
		Dir:       opts.BinDir,
		Path:      opts.Path,
		Verify:    Verify(opts.Root),
	})
	if err != nil {
		return Result{}, err
	}
	return Result{Dir: res.Dir}, nil
}

// takesOver returns the directory of a dotnet on PATH from another root that
// linking would replace, because no preferred directory comes ahead of it.
func takesOver(dirs []string, root string) (string, bool) {
	existing := pathlink.IndexOf(dirs, "dotnet")
	if existing < 0 || sameDir(dirs[existing], root) {
		return "", false
	}
	preferred := pathlink.UserBinDirs()
	dir, _ := pathlink.ChooseDir(dirs, existing, preferred, pathlink.Writable)
	if dir != dirs[existing] || slices.ContainsFunc(preferred, func(p string) bool { return sameDir(p, dir) }) {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(dir, "dotnet.exe")); err != nil {
		return "", false
	}
	return dir, true
}

// Verify returns a pathlink check that bin runs the .NET installed at root.
func Verify(root string) func(bin string) error {
	return func(bin string) error {
		got, err := Probe(bin)
		if err != nil {
			return err
		}
		if !sameDir(got, root) {
			return fmt.Errorf("runs the .NET at %s, not %s", got, root)
		}
		return nil
	}
}

// Probe runs dotnet --list-runtimes and returns the root its runtimes live in.
func Probe(bin string) (string, error) {
	out, err := pathlink.Command(bin, "--list-runtimes").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("does not run: %w: %s", err, strings.TrimSpace(string(out)))
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		start, end := strings.LastIndexByte(line, '['), strings.LastIndexByte(line, ']')
		if start < 0 || end < start {
			continue
		}
		return filepath.Dir(filepath.Dir(line[start+1 : end])), nil
	}
	return "", errors.New("lists no runtimes")
}

// ToolsDir is where dotnet tool install --global puts tools.
func ToolsDir() (string, error) {
	home := os.Getenv("DOTNET_CLI_HOME")
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".dotnet", "tools"), nil
}

// Export appends vars and PATH entries to $BASH_ENV for later bash steps.
// On Windows a PATH entry is converted with cygpath when the step runs, so
// both MSYS and Cygwin bash get the form they expect.
func Export(vars map[string]string, path []string) error {
	if os.Getenv("BASH_ENV") == "" {
		return errors.New("BASH_ENV is not set")
	}
	quoted := make(map[string]string, len(vars)+1)
	for k, v := range vars {
		if runtime.GOOS == "windows" {
			v = filepath.ToSlash(v)
		}
		quoted[k] = shellQuote(v)
	}
	if len(path) > 0 {
		entries := make([]string, 0, len(path))
		for _, p := range path {
			entries = append(entries, shellPath(p))
		}
		quoted["PATH"] = strings.Join(entries, ":") + `:"$PATH"`
	}
	return bashenv.ExportEnvVars(quoted)
}

func shellPath(p string) string {
	if runtime.GOOS != "windows" {
		return shellQuote(p)
	}
	return fmt.Sprintf(`"$(cygpath -u %s 2>/dev/null || printf '%%s' %s)"`, shellQuote(p), shellQuote(unixPath(p)))
}

// unixPath turns C:\Users\x into /c/Users/x, the MSYS form.
func unixPath(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	if len(p) >= 2 && p[1] == ':' {
		return "/" + strings.ToLower(p[:1]) + p[2:]
	}
	return p
}

func shellQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

func sameDir(a, b string) bool {
	a, b = realPath(a), realPath(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// RegisterDir holds .NET's registered install locations on Linux and macOS.
const RegisterDir = "/etc/dotnet"

// Register records root as the .NET install location for arch, which is where
// an apphost started from any shell looks when DOTNET_ROOT is not set. It
// writes dir/install_location_<arch>, as root or with sudo -n, and returns
// that path. Windows keeps the location in the registry instead, so there it
// does nothing.
func Register(dir, root, arch string) (string, error) {
	if runtime.GOOS == "windows" {
		return "", nil
	}
	path := filepath.Join(dir, "install_location_"+arch)
	if b, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(b)) == root { //nolint:gosec // a fixed system path
		return path, nil
	}
	content := root + "\n"
	if err := os.MkdirAll(dir, 0o755); err == nil { //nolint:gosec // the location is world-readable by design
		if err := os.WriteFile(path, []byte(content), 0o644); err == nil { //nolint:gosec // as above
			return path, nil
		}
	}
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return "", fmt.Errorf("writing %s needs root", path)
	}
	cmd := exec.Command(sudo, "-n", "sh", "-c", `mkdir -p "$(dirname "$1")" && cat > "$1"`, "sh", path) //nolint:gosec // fixed arguments
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("writing %s with sudo: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	return path, nil
}
