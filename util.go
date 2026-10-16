package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/circleci-functions/toolkit/pathlink"
)

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "setup-dotnet: "+format+"\n", args...)
}

// dotnet runs the dotnet in root, with its output on this process's.
func dotnet(root string, args ...string) *exec.Cmd {
	name := "dotnet"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return dotnetCmd(filepath.Join(root, name), args...)
}

// dotnetCmd runs bin, which may be a Windows shim, without the first-run banner.
func dotnetCmd(bin string, args ...string) *exec.Cmd {
	cmd := pathlink.Command(bin, args...)
	cmd.Env = append(os.Environ(), "DOTNET_NOLOGO=1", "DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd
}

func lookDotnet() (string, error) {
	return exec.LookPath("dotnet")
}

// splitList splits a flag value on commas and newlines.
func splitList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// expandHome resolves a leading ~, which kong leaves alone.
func expandHome(path string) string {
	if path == "" || path[0] != '~' || (len(path) > 1 && !os.IsPathSeparator(path[1])) {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
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
