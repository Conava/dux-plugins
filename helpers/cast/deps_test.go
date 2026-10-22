package main

import (
	"strings"
	"testing"
)

func TestFamilyOf(t *testing.T) {
	for release, want := range map[string]string{
		"ID=cachyos\nID_LIKE=arch":                              "arch",
		"ID=manjaro\nID_LIKE=arch":                              "arch",
		"ID=ubuntu\nID_LIKE=debian":                             "debian",
		"ID=linuxmint\nID_LIKE=\"ubuntu debian\"":               "debian",
		"ID=pop\nID_LIKE=\"ubuntu debian\"":                     "debian",
		"ID=fedora":                                             "fedora",
		"ID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"":          "fedora",
		"ID=\"opensuse-tumbleweed\"\nID_LIKE=\"opensuse suse\"": "opensuse",
		"ID=gentoo": "other",
		"ID=nixos":  "other",
		"ID=void":   "other",
		"":          "other",
	} {
		if got := familyOf(strings.NewReader(release)); got != want {
			t.Errorf("%q: got %s, want %s", release, got, want)
		}
	}
}
