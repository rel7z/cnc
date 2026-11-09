package version

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
)

var componentToken = regexp.MustCompile(`^[a-z0-9_-]+$`)

// Compare uses SemVer precedence without converting arbitrarily large numbers
// into machine integers. Build metadata is intentionally outside our grammar.
func Compare(a, b SemanticVersion) int {
	ar, br := a.Revision, b.Revision
	if ar == "" {
		ar = "0"
	}
	if br == "" {
		br = "0"
	}
	for _, pair := range [][2]string{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}, {ar, br}} {
		if n := compareNumber(pair[0], pair[1]); n != 0 {
			return n
		}
	}
	if a.PatchSuffix != b.PatchSuffix {
		if a.PatchSuffix == "" {
			return -1
		}
		if b.PatchSuffix == "" {
			return 1
		}
		return strings.Compare(a.PatchSuffix, b.PatchSuffix)
	}
	if len(a.Prerelease) == 0 && len(b.Prerelease) == 0 {
		return 0
	}
	if len(a.Prerelease) == 0 {
		return 1
	}
	if len(b.Prerelease) == 0 {
		return -1
	}
	for i := 0; i < len(a.Prerelease) && i < len(b.Prerelease); i++ {
		x, y := a.Prerelease[i], b.Prerelease[i]
		xn, yn := numeric(x), numeric(y)
		var n int
		switch {
		case xn && yn:
			n = compareNumber(x, y)
		case xn:
			n = -1
		case yn:
			n = 1
		default:
			n = strings.Compare(x, y)
		}
		if n != 0 {
			return n
		}
	}
	if len(a.Prerelease) < len(b.Prerelease) {
		return -1
	}
	if len(a.Prerelease) > len(b.Prerelease) {
		return 1
	}
	return 0
}

func compareNumber(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func ValidateComponent(component string) error {
	p := strings.Split(component, ":")
	if component == "wordpress:core" || component == "joomla:core" || component == "php:php-src" {
		return nil
	}
	if len(p) == 3 && p[0] == "wordpress" && p[1] == "plugin" && componentToken.MatchString(p[2]) {
		return nil
	}
	if len(p) >= 3 && p[0] == "joomla" {
		if p[1] == "plugin" && len(p) == 4 && componentToken.MatchString(p[2]) && componentToken.MatchString(p[3]) && !strings.HasPrefix(p[3], "plg_") {
			return nil
		}
		if len(p) == 3 && componentToken.MatchString(p[2]) {
			prefix := map[string]string{"component": "com_", "module": "mod_", "library": "lib_", "package": "pkg_", "template": "tpl_"}
			if leading, ok := prefix[p[1]]; ok && !strings.HasPrefix(p[2], leading) {
				return nil
			}
		}
	}
	return fmt.Errorf("invalid component %q", component)
}

func clauseMatches(v SemanticVersion, c VersionClause) bool {
	b, err := ParseVersion(c.Version)
	if err != nil {
		return false
	}
	n := Compare(v, b)
	switch c.Operator {
	case "=":
		return n == 0
	case "<":
		return n < 0
	case "<=":
		return n <= 0
	case ">":
		return n > 0
	case ">=":
		return n >= 0
	}
	return false
}

// NormalizeRules validates the entire set before returning any usable rules.
// Normalized clause sets are sorted and deduplicated for duplicate detection.
func NormalizeRules(input []VersionRule) ([]VersionRule, error) {
	ids, names, sets := map[string]bool{}, map[string]bool{}, map[string]bool{}
	out := make([]VersionRule, 0, len(input))
	for _, rule := range input {
		if strings.TrimSpace(rule.ID) == "" || strings.TrimSpace(rule.Name) == "" || ids[rule.ID] || names[rule.Name] {
			return nil, fmt.Errorf("rule IDs and names must be nonempty and unique")
		}
		if err := ValidateComponent(rule.Component); err != nil {
			return nil, err
		}
		if len(rule.Clauses) == 0 {
			return nil, fmt.Errorf("rule %s has no clauses", rule.ID)
		}
		clauses, seen := []VersionClause{}, map[string]bool{}
		for _, c := range rule.Clauses {
			switch c.Operator {
			case "=", "<", "<=", ">", ">=":
			default:
				return nil, fmt.Errorf("rule %s has an unsupported operator", rule.ID)
			}
			v, err := NormalizeVersion(c.Version)
			if err != nil {
				return nil, fmt.Errorf("rule %s: %w", rule.ID, err)
			}
			c.Version = v
			key := c.Operator + " " + v
			if !seen[key] {
				clauses = append(clauses, c)
				seen[key] = true
			}
		}
		sort.Slice(clauses, func(i, j int) bool {
			if clauses[i].Operator == clauses[j].Operator {
				return clauses[i].Version < clauses[j].Version
			}
			return clauses[i].Operator < clauses[j].Operator
		})
		rule.Clauses = clauses
		if !consistent(clauses) {
			return nil, fmt.Errorf("rule %s has contradictory clauses", rule.ID)
		}
		data, _ := json.Marshal(clauses)
		key := rule.Component + string(data)
		if sets[key] {
			return nil, fmt.Errorf("duplicate component and clause set")
		}
		ids[rule.ID], names[rule.Name], sets[key] = true, true, true
		out = append(out, rule)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func consistent(clauses []VersionClause) bool {
	var lower, upper *SemanticVersion
	var lowerOpen, upperOpen bool
	for _, c := range clauses {
		v, _ := ParseVersion(c.Version)
		if c.Operator == "=" {
			for _, other := range clauses {
				if !clauseMatches(v, other) {
					return false
				}
			}
			return true
		}
		if c.Operator == ">" || c.Operator == ">=" {
			if lower == nil || Compare(v, *lower) > 0 {
				value := v
				lower = &value
				lowerOpen = c.Operator == ">"
			} else if Compare(v, *lower) == 0 {
				lowerOpen = lowerOpen || c.Operator == ">"
			}
		} else {
			if upper == nil || Compare(v, *upper) < 0 {
				value := v
				upper = &value
				upperOpen = c.Operator == "<"
			} else if Compare(v, *upper) == 0 {
				upperOpen = upperOpen || c.Operator == "<"
			}
		}
	}
	if upper == nil {
		return true
	}
	minimum := SemanticVersion{Major: "0", Minor: "0", Patch: "0", Revision: "0", Prerelease: []string{"0"}}
	if lower != nil {
		minimum = *lower
		if lowerOpen {
			if len(minimum.Prerelease) > 0 {
				minimum.Prerelease = append(append([]string{}, minimum.Prerelease...), "0")
			} else if minimum.PatchSuffix != "" {
				minimum.PatchSuffix += "a"
			} else {
				n, _ := new(big.Int).SetString(minimum.Revision, 10)
				minimum.Revision = n.Add(n, big.NewInt(1)).String()
				minimum.Prerelease = []string{"0"}
			}
		}
	}
	n := Compare(minimum, *upper)
	return n < 0 || (n == 0 && !upperOpen)
}

func MatchRules(observation VersionObservation, rules []VersionRule) []MatchedRule {
	out := []MatchedRule{}
	if observation.Version == nil || observation.SelectedEvidence == nil || (observation.Confidence != ConfidenceAuthoritative && observation.Confidence != ConfidenceValidatedFallback && observation.Confidence != ConfidenceReported) {
		return out
	}
	v, err := ParseVersion(*observation.Version)
	if err != nil {
		return out
	}
	for _, rule := range rules {
		if rule.Component != observation.Component || len(rule.Clauses) == 0 {
			continue
		}
		matches := true
		for _, c := range rule.Clauses {
			if !clauseMatches(v, c) {
				matches = false
				break
			}
		}
		if matches {
			out = append(out, MatchedRule{ID: rule.ID, Name: rule.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
