// Package dotnetdist installs .NET SDK and runtime archives into a .NET root.
package dotnetdist

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// Platform is an OS and a .NET architecture name: x64, x86, arm64 or arm.
type Platform struct {
	OS   string
	Arch string
	Musl bool
}

var archNames = map[string]string{"amd64": "x64", "386": "x86", "arm64": "arm64", "arm": "arm"}

// muslLoader matches the dynamic loader a musl distribution such as Alpine
// ships, which glibc ones do not.
var muslLoader = "/lib/ld-musl-*.so.1"

// Current returns the platform this process runs on.
func Current() Platform {
	p := Platform{OS: runtime.GOOS, Arch: archNames[runtime.GOARCH]}
	if p.OS == "linux" {
		matches, _ := filepath.Glob(muslLoader)
		p.Musl = len(matches) > 0
	}
	return p
}

// ParseArch normalises an architecture name. amd64 is accepted for x64.
func ParseArch(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "x64", "amd64":
		return "x64", nil
	case "x86":
		return "x86", nil
	case "arm64":
		return "arm64", nil
	case "arm":
		return "arm", nil
	}
	return "", fmt.Errorf("unsupported architecture %q: use x64, x86, arm64 or arm", s)
}

// RID is the .NET runtime identifier archives are published under, such as
// linux-musl-x64, osx-arm64 or win-x86.
func (p Platform) RID() (string, error) {
	if p.Arch == "" {
		return "", fmt.Errorf("unsupported platform %s/%s", p.OS, runtime.GOARCH)
	}
	switch p.OS {
	case "linux":
		if p.Arch == "x86" {
			break
		}
		if p.Musl {
			return "linux-musl-" + p.Arch, nil
		}
		return "linux-" + p.Arch, nil
	case "darwin":
		if p.Arch == "x64" || p.Arch == "arm64" {
			return "osx-" + p.Arch, nil
		}
	case "windows":
		if p.Arch != "arm" {
			return "win-" + p.Arch, nil
		}
	}
	return "", fmt.Errorf(".NET publishes no build for %s/%s", p.OS, p.Arch)
}

// Ext is the archive extension published for the platform.
func (p Platform) Ext() string {
	if p.OS == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// Exe returns name with the platform's executable suffix.
func (p Platform) Exe(name string) string {
	if p.OS == "windows" {
		return name + ".exe"
	}
	return name
}
