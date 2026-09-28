package version

import (
	"strconv"
	"strings"
)

// Release is a release version MAJOR.MINOR.PATCH, the only form release tags
// take (v1.2.3, .github/workflows/release.yml) and the form a relay's
// ready.min_client names (Docs/protocol/envelope.md §ready).
type Release struct {
	Major, Minor, Patch int
}

func (r Release) String() string {
	return strconv.Itoa(r.Major) + "." + strconv.Itoa(r.Minor) + "." + strconv.Itoa(r.Patch)
}

// ParseRelease parses "MAJOR.MINOR.PATCH" (decimal, no leading zeros except
// "0", no "v" prefix, no pre-release or build suffix). It reports false for
// anything else, including dev versions such as "0.0.0-dev+a1b2c3d".
func ParseRelease(s string) (Release, bool) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Release{}, false
	}
	var n [3]int
	for i, p := range parts {
		if p == "" || len(p) > 9 || (len(p) > 1 && p[0] == '0') {
			return Release{}, false
		}
		for _, c := range p {
			if c < '0' || c > '9' {
				return Release{}, false
			}
		}
		n[i], _ = strconv.Atoi(p)
	}
	return Release{n[0], n[1], n[2]}, true
}

// Compare returns -1, 0 or +1 as r is older than, equal to or newer than o.
func (r Release) Compare(o Release) int {
	for _, d := range [3]int{r.Major - o.Major, r.Minor - o.Minor, r.Patch - o.Patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	return 0
}

// MeetsMinimum reports whether version v satisfies minimum (both as
// strings, as carried by ready.min_client and Version). ok is false when
// either is not a release version (a dev build, or a malformed min): the
// caller cannot tell, and must say so rather than guess.
func MeetsMinimum(v, minimum string) (meets, ok bool) {
	rv, ok1 := ParseRelease(v)
	rm, ok2 := ParseRelease(minimum)
	if !ok1 || !ok2 {
		return false, false
	}
	return rv.Compare(rm) >= 0, true
}
