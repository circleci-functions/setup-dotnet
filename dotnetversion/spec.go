package dotnetversion

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Kind is the form a version spec takes.
type Kind int

const (
	// Exact is A.B.C, with an optional prerelease.
	Exact Kind = iota
	// Channel is A.B, A.B.x or A.B.*: the newest release of that channel.
	Channel
	// Major is A, A.x or A.*: the newest release of that major version.
	Major
	// Band is A.B.Cxx: the newest SDK in that feature band.
	Band
	// Latest is the newest release, narrowed by a channel and quality.
	Latest
)

// Spec is a parsed version spec, as dotnet-version and runtime-version take it.
type Spec struct {
	Kind         Kind
	Major, Minor int
	Band         int
	Version      Version
	raw          string
}

var (
	exactRe   = regexp.MustCompile(`^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$`)
	channelRe = regexp.MustCompile(`^(\d+)\.(\d+)(\.[xX*])?$`)
	majorRe   = regexp.MustCompile(`^(\d+)(\.[xX*])?$`)
	bandRe    = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d)xx$`)
)

// ParseSpec parses one version spec.
func ParseSpec(s string) (Spec, error) {
	s = strings.TrimSpace(s)
	spec := Spec{raw: s}
	switch {
	case strings.EqualFold(s, "latest"):
		spec.Kind = Latest
	case exactRe.MatchString(s):
		v, err := Parse(s)
		if err != nil {
			return Spec{}, err
		}
		spec.Kind, spec.Version, spec.Major, spec.Minor = Exact, v, v.Major, v.Minor
	case bandRe.MatchString(s):
		m := bandRe.FindStringSubmatch(s)
		spec.Kind, spec.Major, spec.Minor, spec.Band = Band, atoi(m[1]), atoi(m[2]), atoi(m[3])
	case channelRe.MatchString(s):
		m := channelRe.FindStringSubmatch(s)
		spec.Kind, spec.Major, spec.Minor = Channel, atoi(m[1]), atoi(m[2])
	case majorRe.MatchString(s):
		m := majorRe.FindStringSubmatch(s)
		spec.Kind, spec.Major = Major, atoi(m[1])
	default:
		return Spec{}, fmt.Errorf("invalid version %q: use A.B.C, A.B, A.B.x, A, A.x, A.B.Cxx or latest", s)
	}
	return spec, nil
}

// ParseSpecs parses a list of specs separated by commas, newlines or spaces.
func ParseSpecs(s string) ([]Spec, error) {
	var specs []Spec
	for _, f := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	}) {
		spec, err := ParseSpec(f)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

func (s Spec) String() string {
	return s.raw
}

// Matches reports whether v satisfies the spec. Latest matches everything.
func (s Spec) Matches(v Version) bool {
	switch s.Kind {
	case Exact:
		return v.Compare(s.Version) == 0
	case Channel:
		return v.Major == s.Major && v.Minor == s.Minor
	case Major:
		return v.Major == s.Major
	case Band:
		return v.Major == s.Major && v.Minor == s.Minor && v.Band() == s.Band
	}
	return true
}

// Floating reports whether the spec can match more than one version.
func (s Spec) Floating() bool {
	return s.Kind != Exact
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
