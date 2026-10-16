// Package nuget caches the NuGet global packages folder and authenticates
// package sources.
package nuget

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/circleci-functions/setup-dotnet/internal/cachekey"
)

// rotation is how often the cache keys start over. A restore reaches back to
// another entry only within the window, and that bound is what stops an
// entry carrying every package it ever held into the next one.
const rotation = 72 * time.Hour

// skipDirs are never searched for project files.
var skipDirs = []string{".git", "bin", "obj", "node_modules"}

// DependencyFiles returns the files under dir whose contents decide which
// packages a restore needs, as slash-separated paths relative to dir.
// patterns, when given, are globs that replace the search. Otherwise it is
// every packages.lock.json, plus the tool manifest; or, with no lock file,
// every project file and the MSBuild and NuGet files around them.
func DependencyFiles(dir string, patterns []string) ([]string, error) {
	fsys := os.DirFS(dir)
	if len(patterns) > 0 {
		var files []string
		for _, p := range patterns {
			matches, err := doublestar.Glob(fsys, filepath.ToSlash(p), doublestar.WithFilesOnly())
			if err != nil {
				return nil, fmt.Errorf("cache-dependency-path %q: %w", p, err)
			}
			files = append(files, matches...)
		}
		slices.Sort(files)
		return slices.Compact(files), nil
	}

	var locks, projects []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != "." && slices.Contains(skipDirs, d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		switch name := strings.ToLower(d.Name()); {
		case name == "packages.lock.json":
			locks = append(locks, p)
		case name == "dotnet-tools.json" && path.Base(path.Dir(p)) == ".config":
			locks = append(locks, p)
			projects = append(projects, p)
		case isProjectFile(name):
			projects = append(projects, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(locks, func(p string) bool { return path.Base(p) == "packages.lock.json" }) {
		return locks, nil
	}
	return projects, nil
}

func isProjectFile(name string) bool {
	switch name {
	case "directory.packages.props", "directory.build.props", "directory.build.targets", "nuget.config", "global.json", "packages.config":
		return true
	}
	ext := path.Ext(name)
	return ext == ".csproj" || ext == ".fsproj" || ext == ".vbproj"
}

// Hash digests files, names included, so a moved project changes it too.
func Hash(dir string, files []string) (string, error) {
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "%s\x00", f)
		file, err := os.Open(filepath.Join(dir, filepath.FromSlash(f))) //nolint:gosec // a file found under the project
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, file)
		_ = file.Close()
		if err != nil {
			return "", err
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Keys returns the key to save dir under and the keys to restore from, best
// first: the same dependencies, then any entry from the same window.
func Keys(osArch, dir, hash string, now time.Time) (string, []string) {
	secs := int64(rotation.Seconds())
	window := fmt.Sprintf("setup-dotnet-nuget-v1-%s-%s-%d-", osArch, cachekey.Path(dir), now.Unix()/secs*secs)
	return window + hash, []string{window + hash, window}
}

// GlobalPackagesDir asks dotnet where the global packages folder is, which
// honours NUGET_PACKAGES and any nuget.config setting.
func GlobalPackagesDir(cmd *exec.Cmd) (string, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("dotnet nuget locals: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if _, dir, ok := strings.Cut(sc.Text(), "global-packages:"); ok {
			return filepath.Clean(strings.TrimSpace(dir)), nil
		}
	}
	return "", errors.New("dotnet nuget locals printed no global-packages folder")
}

// HasLockFile reports whether dir or a directory under it holds a
// packages.lock.json.
func HasLockFile(dir string) bool {
	found := false
	_ = fs.WalkDir(os.DirFS(dir), ".", func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return fs.SkipDir
		case d.IsDir() && p != "." && slices.Contains(skipDirs, d.Name()):
			return fs.SkipDir
		case !d.IsDir() && strings.EqualFold(d.Name(), "packages.lock.json"):
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}
