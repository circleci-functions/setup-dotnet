// Package globaljson reads global.json and picks the SDK it asks for the way
// the .NET SDK resolver does.
package globaljson

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/circleci-functions/setup-dotnet/dotnetversion"
)

// File is the part of a global.json that selects an SDK and a test runner.
type File struct {
	Path string
	SDK  struct {
		Version         string `json:"version"`
		RollForward     string `json:"rollForward"`
		AllowPrerelease *bool  `json:"allowPrerelease"`
	} `json:"sdk"`
	Test struct {
		Runner string `json:"runner"`
	} `json:"test"`
}

// Find returns the global.json in dir or the nearest parent, as the SDK looks
// for one, or "" when there is none.
func Find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		p := filepath.Join(dir, "global.json")
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// Load reads path. Comments and trailing commas are allowed, as the SDK
// allows them.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the caller names the file
	if err != nil {
		return nil, err
	}
	f := &File{Path: path}
	if err := json.Unmarshal(StripJSONC(data), f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return f, nil
}

func (f *File) HasSDK() bool {
	return f.SDK.Version != "" || f.SDK.RollForward != ""
}

// UsesTestingPlatform reports whether dotnet test runs Microsoft.Testing.Platform
// rather than VSTest.
func (f *File) UsesTestingPlatform() bool {
	return strings.EqualFold(f.Test.Runner, "Microsoft.Testing.Platform")
}

// Policy is a parsed rollForward value.
type Policy string

const (
	Patch         Policy = "patch"
	Feature       Policy = "feature"
	Minor         Policy = "minor"
	Major         Policy = "major"
	LatestPatch   Policy = "latestPatch"
	LatestFeature Policy = "latestFeature"
	LatestMinor   Policy = "latestMinor"
	LatestMajor   Policy = "latestMajor"
	Disable       Policy = "disable"
)

var policies = []Policy{Patch, Feature, Minor, Major, LatestPatch, LatestFeature, LatestMinor, LatestMajor, Disable}

type Constraint struct {
	Version         *dotnetversion.Version
	Policy          Policy
	AllowPrerelease bool
}

// Constraint parses the sdk section. With no version the policy defaults to
// latestMajor, otherwise to patch.
func (f *File) Constraint() (Constraint, error) {
	c := Constraint{AllowPrerelease: f.SDK.AllowPrerelease != nil && *f.SDK.AllowPrerelease}
	if f.SDK.Version != "" {
		v, err := dotnetversion.Parse(f.SDK.Version)
		if err != nil {
			return c, fmt.Errorf("%s: sdk.version %q must be a full SDK version, such as 8.0.100", f.Path, f.SDK.Version)
		}
		c.Version = &v
	}
	switch {
	case f.SDK.RollForward != "":
		i := slices.IndexFunc(policies, func(p Policy) bool { return strings.EqualFold(string(p), f.SDK.RollForward) })
		if i < 0 {
			return c, fmt.Errorf("%s: unknown sdk.rollForward %q", f.Path, f.SDK.RollForward)
		}
		c.Policy = policies[i]
	case c.Version == nil:
		c.Policy = LatestMajor
	default:
		c.Policy = Patch
	}
	if c.Version == nil && c.Policy != LatestMajor {
		return c, fmt.Errorf("%s: sdk.rollForward %q needs an sdk.version", f.Path, c.Policy)
	}
	return c, nil
}

// Channels reports whether an SDK from channel major.minor could satisfy c,
// so a resolver knows which channels to read.
func (c Constraint) Channels(major, minor int) bool {
	if c.Version == nil {
		return true
	}
	v := *c.Version
	switch c.Policy {
	case Minor, LatestMinor:
		return major == v.Major && minor >= v.Minor
	case Major, LatestMajor:
		return major > v.Major || (major == v.Major && minor >= v.Minor)
	}
	return major == v.Major && minor == v.Minor
}

// Select orders the candidates c accepts, best first: the SDK the resolver
// would pick if all of them were installed, then the next best. A prerelease
// sdk.version is only met exactly.
func (c Constraint) Select(candidates []dotnetversion.Version) []dotnetversion.Version {
	var ok []dotnetversion.Version
	for _, v := range candidates {
		if c.accepts(v) {
			ok = append(ok, v)
		}
	}
	slices.SortFunc(ok, c.compare)
	return ok
}

func (c Constraint) accepts(v dotnetversion.Version) bool {
	if c.Version == nil {
		return !v.IsPrerelease() || c.AllowPrerelease
	}
	want := *c.Version
	if c.Policy == Disable || want.IsPrerelease() {
		return v.Compare(want) == 0
	}
	if v.IsPrerelease() && !c.AllowPrerelease {
		return false
	}
	if v.Compare(want) < 0 {
		return false
	}
	switch c.Policy {
	case Patch, LatestPatch:
		return v.Major == want.Major && v.Minor == want.Minor && v.Band() == want.Band()
	case Feature, LatestFeature:
		return v.Major == want.Major && v.Minor == want.Minor
	case Minor, LatestMinor:
		return v.Major == want.Major
	}
	return true
}

// compare orders accepted versions by preference. The latest* policies take
// the highest. The others prefer the nearest feature band, minor or major,
// and the highest patch within it; patch prefers the exact version.
func (c Constraint) compare(a, b dotnetversion.Version) int {
	if c.Version != nil && !strings.HasPrefix(string(c.Policy), "latest") {
		want := *c.Version
		if c.Policy == Patch {
			if d := boolRank(b.Compare(want) == 0) - boolRank(a.Compare(want) == 0); d != 0 {
				return d
			}
		}
		if d := bandKey(a) - bandKey(b); d != 0 {
			return d
		}
	}
	return b.Compare(a)
}

func bandKey(v dotnetversion.Version) int {
	return (v.Major*1000+v.Minor)*100 + v.Band()
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}
