/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/
package version

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Version type definition for handling simple version comparision
type Version struct {
	major, minor uint
}

// NewVersion creates a new version object for given
// major and minor version values
func NewVersion(major, minor uint) Version {
	return Version{
		major: major,
		minor: minor,
	}
}

// Parse creates a new version with major/minor from the given string, which must
// have the format <major>.<minor>. Error texts do not include the version string
// itself.
func Parse(version string) (Version, error) {
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return Version{}, errors.New("must have <major>.<minor> format")
	}
	major, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return Version{}, fmt.Errorf("major version %q: %v", parts[0], err)
	}
	minor, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return Version{}, fmt.Errorf("minor version %q: %v", parts[1], err)
	}
	return NewVersion(uint(major), uint(minor)), nil
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d", v.major, v.minor)
}

// Major returns major version of v
func (v Version) Major() uint {
	return v.major
}

// Minor returns minor version of v
func (v Version) Minor() uint {
	return v.minor
}

// Compare compares v with given otherVersion
// Returns,
//  0 if two versions are same
//  >0 if v is greater otherVersion
//  <0 if v is less than otherVersion
func (v Version) Compare(major, minor uint) int {
	d := int(v.major - major)
	if d == 0 {
		d = int(v.minor - minor)
	}

	return d
}
