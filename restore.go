package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/circleci-functions/setup-dotnet/nuget"
)

type restoreCmd struct {
	Project     string `help:"Solution, project or directory to restore. Empty lets dotnet find one in the working directory."`
	LockedMode  bool   `default:"true" negatable:"" help:"Pass --locked-mode when a packages.lock.json exists, so a restore that would change a lock file fails."`
	ToolRestore bool   `default:"true" negatable:"" help:"Also run dotnet tool restore when a .config/dotnet-tools.json exists."`
	ExtraArgs   string `help:"Extra arguments for dotnet restore, split as a shell would but never evaluated."`
}

func (c *restoreCmd) Run() error {
	bin, err := lookDotnet()
	if err != nil {
		return fmt.Errorf("no dotnet on PATH: run setup-dotnet first")
	}
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	extra, err := shellWords(c.ExtraArgs)
	if err != nil {
		return fmt.Errorf("--extra-args: %w", err)
	}

	args := []string{"restore"}
	if c.Project != "" {
		args = append(args, c.Project)
	}
	if c.LockedMode && nuget.HasLockFile(projectDir(wd, c.Project)) {
		args = append(args, "--locked-mode")
	}
	args = append(args, extra...)
	if err := runDotnet(bin, wd, args...); err != nil {
		return err
	}

	if c.ToolRestore && hasToolManifest(wd) {
		return runDotnet(bin, wd, "tool", "restore")
	}
	return nil
}

// projectDir is the directory a restore or test of project works in.
func projectDir(wd, project string) string {
	if project == "" {
		return wd
	}
	p := project
	if !filepath.IsAbs(p) {
		p = filepath.Join(wd, p)
	}
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		return p
	}
	return filepath.Dir(p)
}

// hasToolManifest reports whether dotnet tool restore would find a manifest
// from dir: .config/dotnet-tools.json or dotnet-tools.json, there or above.
func hasToolManifest(dir string) bool {
	for {
		for _, p := range []string{filepath.Join(dir, ".config", "dotnet-tools.json"), filepath.Join(dir, "dotnet-tools.json")} {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

func runDotnet(bin, dir string, args ...string) error {
	cmd := dotnetCmd(bin, args...)
	cmd.Dir = dir
	logf("dotnet %s", joinArgs(args))
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("dotnet %s: %w", args[0], err)
	}
	return nil
}
