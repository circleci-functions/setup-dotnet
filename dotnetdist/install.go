package dotnetdist

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/circleci-functions/setup-dotnet/dotnetversion"
	"github.com/circleci-functions/setup-dotnet/internal/cachekey"
)

// Cache keeps downloaded archives between jobs.
type Cache interface {
	Restore(ctx context.Context, key string)
	Save(ctx context.Context, key string, paths []string)
}

// Installer installs archives into one .NET root.
type Installer struct {
	Root     Root
	CacheDir string
	// Cache is nil when archives are not cached.
	Cache Cache
	// CacheKeySuffix partitions the cache keys.
	CacheKeySuffix string
	HTTP           *http.Client
	Log            io.Writer
}

// Install puts a into the root unless the root already has it, and reports
// whether it did. A download that 404s returns an error wrapping ErrNotFound.
func (i *Installer) Install(ctx context.Context, a dotnetversion.Artifact) (bool, error) {
	if i.Root.Has(a.Component, a.Version) {
		return false, nil
	}
	archivePath, err := i.fetch(ctx, a)
	if err != nil {
		return false, err
	}
	defer os.Remove(archivePath)

	if err := os.MkdirAll(i.Root.Dir(), 0o755); err != nil { //nolint:gosec // an SDK is world-readable
		return false, err
	}
	staging, err := os.MkdirTemp(i.Root.Dir(), ".setup-dotnet-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(staging)

	if err := extract(archivePath, staging); err != nil {
		return false, err
	}
	kept, err := merge(staging, i.Root.Dir(), i.newestHost(a.HostVersion))
	if err != nil {
		return false, err
	}
	if len(kept) > 0 {
		fmt.Fprintf(i.Log, "setup-dotnet: kept the existing %v in %s, which could not be replaced\n", kept, i.Root.Dir())
	}
	if !i.Root.Has(a.Component, a.Version) {
		return false, fmt.Errorf("%s did not install %s %s", a.FileName(), a.Component, a.Version)
	}
	return true, nil
}

func (i *Installer) fetch(ctx context.Context, a dotnetversion.Artifact) (string, error) {
	archivePath := filepath.Join(i.CacheDir, a.FileName())
	key := "setup-dotnet-archive-v1-" + a.FileName() + "-" + cachekey.Path(archivePath) + i.CacheKeySuffix
	if i.Cache != nil && !verify(archivePath, a.SHA512) {
		i.Cache.Restore(ctx, key)
	}
	if verify(archivePath, a.SHA512) {
		return archivePath, nil
	}
	fmt.Fprintf(i.Log, "setup-dotnet: downloading %s\n", a.URL)
	client := i.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	if err := download(ctx, client, a.URL, archivePath, a.SHA512); err != nil {
		return "", err
	}
	if i.Cache != nil {
		i.Cache.Save(ctx, key, []string{archivePath})
	}
	return archivePath, nil
}

// newestHost reports whether an archive whose host is v carries a host at
// least as new as the root's. An unknown version, as for a daily build, is
// taken to be newest.
func (i *Installer) newestHost(v dotnetversion.Version) bool {
	if v == (dotnetversion.Version{}) {
		return true
	}
	hosts := i.Root.Hosts()
	return len(hosts) == 0 || v.Compare(hosts[0]) >= 0
}

// Root is a .NET installation directory: the one holding dotnet, sdk/ and
// shared/.
type Root string

func (r Root) Dir() string {
	return string(r)
}

// Has reports whether the root holds version v of c.
func (r Root) Has(c dotnetversion.Component, v dotnetversion.Version) bool {
	return slices.ContainsFunc(r.Versions(c), func(have dotnetversion.Version) bool { return have.Compare(v) == 0 })
}

// Versions returns the versions of c installed in the root, newest first.
func (r Root) Versions(c dotnetversion.Component) []dotnetversion.Version {
	switch c {
	case dotnetversion.ASPNETCoreRuntime:
		return r.framework("Microsoft.AspNetCore.App")
	case dotnetversion.WindowsDesktopRuntime:
		return r.framework("Microsoft.WindowsDesktop.App")
	}
	return r.versions(filepath.Join(r.Dir(), "sdk"), "dotnet.dll")
}

// Hosts returns the host versions in the root, newest first.
func (r Root) Hosts() []dotnetversion.Version {
	return r.versions(filepath.Join(r.Dir(), "host", "fxr"), "")
}

func (r Root) framework(name string) []dotnetversion.Version {
	return r.versions(filepath.Join(r.Dir(), "shared", name), name+".deps.json")
}

// versions lists the version-named directories in dir, keeping those that
// hold marker when it is set.
func (r Root) versions(dir, marker string) []dotnetversion.Version {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []dotnetversion.Version
	for _, e := range entries {
		v, err := dotnetversion.Parse(e.Name())
		if err != nil || !e.IsDir() {
			continue
		}
		if marker != "" {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), marker)); err != nil {
				continue
			}
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b dotnetversion.Version) int { return b.Compare(a) })
	return out
}
