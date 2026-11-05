package contract

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// operators supported in required_version constraint lists.
var operators = []string{">=", "<=", ">", "<", "="}

// Satisfies reports whether version (e.g. "0.1.0") satisfies a
// space-separated constraint list like ">=0.1.0 <0.2.0". Development builds
// ("dev" or empty) satisfy every constraint.
func Satisfies(version, constraints string) (bool, error) {
	if version == "" || version == "dev" {
		return true, nil
	}
	v := "v" + strings.TrimPrefix(version, "v")
	if !semver.IsValid(v) {
		return false, fmt.Errorf("invalid version %q", version)
	}
	for _, field := range strings.Fields(constraints) {
		matched := false
		for _, op := range operators {
			if !strings.HasPrefix(field, op) {
				continue
			}
			matched = true
			c := "v" + strings.TrimPrefix(field[len(op):], "v")
			if !semver.IsValid(c) {
				return false, fmt.Errorf("invalid constraint %q", field)
			}
			cmp := semver.Compare(v, c)
			ok := map[string]bool{
				">=": cmp >= 0, "<=": cmp <= 0,
				">": cmp > 0, "<": cmp < 0, "=": cmp == 0,
			}[op]
			if !ok {
				return false, nil
			}
			break
		}
		if !matched {
			return false, fmt.Errorf("unparseable constraint %q", field)
		}
	}
	return true, nil
}
