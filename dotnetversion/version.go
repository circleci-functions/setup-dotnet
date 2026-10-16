// Package dotnetversion parses .NET version specs and resolves them against
// Microsoft's release metadata.
package dotnetversion

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a .NET SDK or runtime version, such as 10.0.402 or
// 11.0.100-rc.2.26504.105.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// Parse parses major.minor.patch with an optional prerelease suffix.
func Parse(s string) (Version, error) {
	core, pre, _ := strings.Cut(strings.TrimSpace(s), "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q", s)
	}
	nums := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return Version{}, fmt.Errorf("invalid version %q", s)
		}
		nums[i] = n
	}
	return Version{Major: nums[0], Minor: nums[1], Patch: nums[2], Pre: pre}, nil
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Channel returns the release channel, e.g. "10.0".
func (v Version) Channel() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

// Band returns the SDK feature band: 4 for 8.0.4xx.
func (v Version) Band() int {
	return v.Patch / 100
}

func (v Version) IsPrerelease() bool {
	return v.Pre != ""
}

// Compare orders versions as semver does: a prerelease sorts before its
// release, and prerelease identifiers compare numerically where both are
// numbers.
func (v Version) Compare(o Version) int {
	for _, d := range []int{v.Major - o.Major, v.Minor - o.Minor, v.Patch - o.Patch} {
		if d != 0 {
			return sign(d)
		}
	}
	switch {
	case v.Pre == o.Pre:
		return 0
	case v.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	}
	return comparePre(v.Pre, o.Pre)
}

func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aErr := strconv.Atoi(as[i])
		bn, bErr := strconv.Atoi(bs[i])
		switch {
		case aErr == nil && bErr == nil:
			if an != bn {
				return sign(an - bn)
			}
		case aErr == nil:
			return -1
		case bErr == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	return sign(len(as) - len(bs))
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}
