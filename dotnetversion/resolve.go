package dotnetversion

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
)

// Component is what an archive installs.
type Component int

const (
	SDK Component = iota
	// ASPNETCoreRuntime carries Microsoft.NETCore.App as well as
	// Microsoft.AspNetCore.App.
	ASPNETCoreRuntime
	// WindowsDesktopRuntime carries Microsoft.NETCore.App as well as
	// Microsoft.WindowsDesktop.App, and exists only for Windows.
	WindowsDesktopRuntime
)

func (c Component) String() string {
	switch c {
	case ASPNETCoreRuntime:
		return "ASP.NET Core Runtime"
	case WindowsDesktopRuntime:
		return "Windows Desktop Runtime"
	}
	return ".NET SDK"
}

// FilePrefix is the archive name's prefix, before the RID.
func (c Component) FilePrefix() string {
	switch c {
	case ASPNETCoreRuntime:
		return "aspnetcore-runtime"
	case WindowsDesktopRuntime:
		return "windowsdesktop-runtime"
	}
	return "dotnet-sdk"
}

type Artifact struct {
	Component Component
	Version   Version
	// HostVersion is the runtime version the archive's host comes from. Zero
	// when unknown, as for daily builds.
	HostVersion Version
	URL         string
	SHA512      string
}

// FileName is the archive's file name, which carries its version and RID.
func (a Artifact) FileName() string {
	return path.Base(a.URL)
}

type Query struct {
	Spec      Spec
	Component Component
	// Quality is "", "ga" or "preview". "ga" leaves out prereleases; "preview"
	// lets latest match them too.
	Quality string
	// Channel narrows Latest: LTS, STS, A.B or A.B.Cxx.
	Channel string
	RID     string
	// Ext is ".tar.gz" or ".zip".
	Ext string
}

// Resolve returns the archives q matches from the newest channel that has
// any, newest first. Later entries are fallbacks for when the newest is not
// downloadable yet.
func (m *Metadata) Resolve(ctx context.Context, q Query) ([]Artifact, error) {
	q, err := q.normalize()
	if err != nil {
		return nil, err
	}
	index, err := m.Index(ctx)
	if err != nil {
		return nil, err
	}
	channels := slices.DeleteFunc(slices.Clone(index), func(c ChannelInfo) bool { return !q.wantsChannel(c) })
	if len(channels) == 0 {
		return nil, fmt.Errorf("no .NET release channel matches %q", q.describe())
	}
	for _, ch := range channels {
		all, err := m.Available(ctx, ch, q.Component, q.RID, q.Ext)
		if err != nil {
			return nil, err
		}
		matches := slices.DeleteFunc(all, func(a Artifact) bool { return !q.wantsVersion(a.Version) })
		if len(matches) > 0 {
			return matches, nil
		}
	}
	return nil, fmt.Errorf("no %s matches %q for %s", q.Component, q.describe(), q.RID)
}

// Available returns every version of comp that channel ch publishes an
// archive of for rid, newest first.
func (m *Metadata) Available(ctx context.Context, ch ChannelInfo, comp Component, rid, ext string) ([]Artifact, error) {
	releases, err := m.channelReleases(ctx, ch)
	if err != nil {
		return nil, err
	}
	name := comp.FilePrefix() + "-" + rid + ext
	seen := map[string]bool{}
	var out []Artifact
	for _, r := range releases {
		var host Version
		if r.Runtime != nil {
			host, _ = Parse(r.Runtime.Version)
		}
		for _, p := range r.products(comp) {
			v, err := Parse(p.Version)
			if err != nil || seen[v.String()] {
				continue
			}
			f, ok := p.file(name)
			if !ok {
				continue
			}
			seen[v.String()] = true
			out = append(out, Artifact{Component: comp, Version: v, HostVersion: host, URL: f.URL, SHA512: strings.ToLower(f.Hash)})
		}
	}
	slices.SortFunc(out, func(a, b Artifact) int { return b.Version.Compare(a.Version) })
	return out, nil
}

func (r release) products(comp Component) []product {
	switch comp {
	case ASPNETCoreRuntime:
		return nonNil(r.AspNetCore)
	case WindowsDesktopRuntime:
		return nonNil(r.WindowsDesktop)
	}
	if len(r.SDKs) > 0 {
		return r.SDKs
	}
	return nonNil(r.SDK)
}

func (p product) file(name string) (file, bool) {
	for _, f := range p.Files {
		if f.Name == name && f.URL != "" && f.Hash != "" {
			return f, true
		}
	}
	return file{}, false
}

func nonNil(p *product) []product {
	if p == nil {
		return nil
	}
	return []product{*p}
}

// normalize turns latest with an A.B or A.B.Cxx channel into that spec, and
// rejects what a runtime cannot be asked for.
func (q Query) normalize() (Query, error) {
	if q.Spec.Kind == Latest && q.Channel != "" && !isSupportChannel(q.Channel) {
		spec, err := ParseSpec(q.Channel)
		if err != nil || (spec.Kind != Channel && spec.Kind != Band) {
			return q, fmt.Errorf("invalid dotnet-channel %q: use LTS, STS, A.B or A.B.Cxx", q.Channel)
		}
		q.Spec, q.Channel = spec, ""
	}
	if q.Spec.Kind == Band && q.Component != SDK {
		return q, fmt.Errorf("invalid runtime version %q: feature bands are for SDKs", q.Spec)
	}
	return q, nil
}

func (q Query) wantsChannel(c ChannelInfo) bool {
	major, minor := c.MajorMinor()
	switch q.Spec.Kind {
	case Exact, Channel, Band:
		return major == q.Spec.Major && minor == q.Spec.Minor
	case Major:
		return major == q.Spec.Major
	}
	if c.SupportPhase == "eol" || (c.Prerelease() && q.Quality != "preview") {
		return false
	}
	switch strings.ToUpper(q.Channel) {
	case "LTS":
		return c.ReleaseType == "lts"
	case "STS":
		return c.ReleaseType == "sts"
	}
	return true
}

func (q Query) wantsVersion(v Version) bool {
	if !q.Spec.Matches(v) {
		return false
	}
	if q.Spec.Kind == Exact || !v.IsPrerelease() {
		return true
	}
	if q.Quality == "ga" {
		return false
	}
	return q.Spec.Kind != Latest || q.Quality == "preview"
}

func (q Query) describe() string {
	if q.Spec.Kind == Latest && q.Channel != "" {
		return "latest " + q.Channel
	}
	return q.Spec.String()
}

func isSupportChannel(s string) bool {
	return strings.EqualFold(s, "LTS") || strings.EqualFold(s, "STS")
}
