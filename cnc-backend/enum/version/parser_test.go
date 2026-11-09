package version

import (
	"errors"
	"reflect"
	"testing"
)

func TestNormalizeVersionAcceptsSupportedForms(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "major", input: "1", want: "1.0.0"},
		{name: "major with v", input: "v2", want: "2.0.0"},
		{name: "major minor", input: "3.14", want: "3.14.0"},
		{name: "major minor with v", input: "v4.5", want: "4.5.0"},
		{name: "full release", input: "6.7.8", want: "6.7.8"},
		{name: "four-part release", input: "3.1.8.10", want: "3.1.8.10"},
		{name: "four-part zero revision", input: "3.1.8.0", want: "3.1.8"},
		{name: "letter patch suffix", input: "2.6.8c", want: "2.6.8c"},
		{name: "full release with v", input: "v9.10.11", want: "9.10.11"},
		{name: "prerelease", input: "1.2.3-alpha.1-rc", want: "1.2.3-alpha.1-rc"},
		{name: "numeric prerelease zero", input: "v1.2.3-0", want: "1.2.3-0"},
		{name: "non-numeric leading zero", input: "1.2.3-01a", want: "1.2.3-01a"},
		{name: "large numeric components", input: "123456789012345678901234567890", want: "123456789012345678901234567890.0.0"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeVersion(test.input)
			if err != nil {
				t.Fatalf("NormalizeVersion(%q) returned error: %v", test.input, err)
			}
			if got != test.want {
				t.Fatalf("NormalizeVersion(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestParseVersionRetainsNormalizedComponentsAndPrereleaseIdentifiers(t *testing.T) {
	got, err := ParseVersion("v12.34.56-alpha.7.rc")
	if err != nil {
		t.Fatalf("ParseVersion returned error: %v", err)
	}

	want := SemanticVersion{
		Major:      "12",
		Minor:      "34",
		Patch:      "56",
		Revision:   "0",
		Prerelease: []string{"alpha", "7", "rc"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseVersion returned %#v, want %#v", got, want)
	}
	if got.String() != "12.34.56-alpha.7.rc" {
		t.Fatalf("String() = %q, want %q", got.String(), "12.34.56-alpha.7.rc")
	}
}

func TestNormalizeVersionRejectsUnsupportedForms(t *testing.T) {
	tests := []string{
		"",
		"v",
		"V1.2.3",
		"1.2.3.4.5",
		"1.2.3.04",
		"1.2.3c-beta",
		"1..2",
		"1.",
		".1",
		"01",
		"1.02",
		"1.2.03",
		"-1.2.3",
		"1.2.-3",
		"1.2.3-alpha+build",
		"1.2.3+build",
		"1-alpha",
		"1.2-beta",
		"1.2.3-",
		"1.2.3-foo..bar",
		"1.2.3-01",
		"1.2.3-1.01",
		"1.2.3-foo_bar",
		"1.2.3-*",
		"1.x.3",
		"1.2.*",
		" 1.2.3",
		"1.2.3 ",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			if _, err := NormalizeVersion(input); !errors.Is(err, ErrInvalidVersion) {
				t.Fatalf("NormalizeVersion(%q) error = %v, want ErrInvalidVersion", input, err)
			}
		})
	}
}
