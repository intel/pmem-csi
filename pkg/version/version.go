/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/
package version

import (
	"fmt"
)

// Version type definition for handling simple version comparision
type Version struct {
	major, minor uint
}

// NewVersion creates a new version object for given
// major and minor version values
func NewVersion(major, minor uint) *Version {
	return &Version{
		major: major,
		minor: minor,
	}
}

func (v *Version) String() string {
	return fmt.Sprintf("%d.%d", v.major, v.minor)
}

// Major returns major version of v
func (v *Version) Major() uint {
	return v.major
}

// Minor returns minor version of v
func (v *Version) Minor() uint {
	return v.minor
}

// Compare compares v with given otherVersion
// Returns,
//  0 if two versions are same
//  >0 if v is greater otherVersion
//  <0 if v is less than otherVersion
func (v *Version) Compare(major, minor uint) int {
	d := int(v.major - major)
	if d == 0 {
		d = int(v.minor - minor)
	}

	return d
}
