package version

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidVersion is returned when an input does not satisfy the supported
// semantic-version grammar.
var ErrInvalidVersion = errors.New("invalid version")

// SemanticVersion is a normalized version accepted by the shared version
// package. Core numeric components are retained as canonical decimal strings
// so valid versions are not limited by machine integer width.
type SemanticVersion struct {
	Major       string
	Minor       string
	Patch       string
	Revision    string
	PatchSuffix string
	Prerelease  []string
}

// Version is the short name for SemanticVersion used by callers that model a
// parsed version directly.
type Version = SemanticVersion

// ParseVersion accepts one to four numeric components and an optional letter
// suffix on a three-part patch, such as 2.6.8c.
func ParseVersion(input string) (SemanticVersion, error) {
	if input == "" {
		return SemanticVersion{}, invalidVersion("version is empty")
	}

	if input[0] == 'v' {
		input = input[1:]
	}
	if input == "" {
		return SemanticVersion{}, invalidVersion("version has no numeric components")
	}
	if strings.IndexByte(input, '+') >= 0 {
		return SemanticVersion{}, invalidVersion("build metadata is not supported")
	}

	core := input
	var prerelease string
	if separator := strings.IndexByte(input, '-'); separator >= 0 {
		core = input[:separator]
		prerelease = input[separator+1:]
		if prerelease == "" {
			return SemanticVersion{}, invalidVersion("prerelease is empty")
		}
	}

	components := strings.Split(core, ".")
	if len(components) < 1 || len(components) > 4 {
		return SemanticVersion{}, invalidVersion("expected one to four numeric components")
	}
	patchSuffix := ""
	if len(components) == 3 {
		patch := components[2]
		for len(patch) > 0 && ((patch[len(patch)-1] >= 'a' && patch[len(patch)-1] <= 'z') || (patch[len(patch)-1] >= 'A' && patch[len(patch)-1] <= 'Z')) {
			patchSuffix = string(patch[len(patch)-1]) + patchSuffix
			patch = patch[:len(patch)-1]
		}
		if patchSuffix != "" {
			if prerelease != "" {
				return SemanticVersion{}, invalidVersion("letter suffix and prerelease cannot be combined")
			}
			components[2] = patch
			patchSuffix = strings.ToLower(patchSuffix)
		}
	}
	for _, component := range components {
		if !validNumericIdentifier(component) {
			return SemanticVersion{}, invalidVersion("numeric components must be non-negative decimal integers without leading zeroes")
		}
	}
	if prerelease != "" && len(components) < 3 {
		return SemanticVersion{}, invalidVersion("prerelease requires major, minor, and patch components")
	}

	parsed := SemanticVersion{
		Major:       components[0],
		Minor:       "0",
		Patch:       "0",
		Revision:    "0",
		PatchSuffix: patchSuffix,
	}
	if len(components) >= 2 {
		parsed.Minor = components[1]
	}
	if len(components) >= 3 {
		parsed.Patch = components[2]
	}
	if len(components) == 4 {
		parsed.Revision = components[3]
	}

	if prerelease != "" {
		parsed.Prerelease = strings.Split(prerelease, ".")
		for _, identifier := range parsed.Prerelease {
			if !validPrereleaseIdentifier(identifier) {
				return SemanticVersion{}, invalidVersion("prerelease identifiers must be non-empty ASCII alphanumeric or hyphen strings")
			}
		}
	}

	return parsed, nil
}

// NormalizeVersion returns the canonical representation of one supported
// version. The optional leading v is removed and omitted minor or patch
// components are filled with zero.
func NormalizeVersion(input string) (string, error) {
	parsed, err := ParseVersion(input)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

// String returns the canonical semantic-version representation.
func (v SemanticVersion) String() string {
	canonical := v.Major + "." + v.Minor + "." + v.Patch
	if v.Revision != "" && v.Revision != "0" {
		canonical += "." + v.Revision
	}
	canonical += v.PatchSuffix
	if len(v.Prerelease) > 0 {
		canonical += "-" + strings.Join(v.Prerelease, ".")
	}
	return canonical
}

func validNumericIdentifier(identifier string) bool {
	if identifier == "" || (len(identifier) > 1 && identifier[0] == '0') {
		return false
	}
	for i := 0; i < len(identifier); i++ {
		if identifier[i] < '0' || identifier[i] > '9' {
			return false
		}
	}
	return true
}

func validPrereleaseIdentifier(identifier string) bool {
	if identifier == "" {
		return false
	}
	for i := 0; i < len(identifier); i++ {
		character := identifier[i]
		if !((character >= '0' && character <= '9') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			character == '-') {
			return false
		}
	}
	return !isNumericIdentifierWithLeadingZero(identifier)
}

func isNumericIdentifierWithLeadingZero(identifier string) bool {
	if len(identifier) <= 1 || identifier[0] != '0' {
		return false
	}
	for i := 1; i < len(identifier); i++ {
		if identifier[i] < '0' || identifier[i] > '9' {
			return false
		}
	}
	return true
}

func invalidVersion(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidVersion, reason)
}
