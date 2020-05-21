/*
Copyright 2020 The Kubernetes Authors.

SPDX-License-Identifier: Apache-2.0
*/
package version_test

import (
	"testing"

	"github.com/intel/pmem-csi/pkg/version"
	"github.com/stretchr/testify/assert"
)

func TestVersion(t *testing.T) {
	t.Run("new version", func(t *testing.T) {
		major, minor := uint(1), uint(6)
		v := version.NewVersion(major, minor)
		assert.Equal(t, "1.6", v.String(), "mismatched version string")
		assert.Equal(t, major, v.Major(), "mismatched major number")
		assert.Equal(t, minor, v.Minor(), "mismatched minor number")

		v = version.NewVersion(1, 160)
		assert.Equal(t, "1.160", v.String(), "mismatched version string")

		v = version.NewVersion(0, 6)
		assert.Equal(t, "0.6", v.String(), "mismatched version string")
	})

	t.Run("version comparison", func(t *testing.T) {
		v := version.NewVersion(1, 10)
		assert.Greater(t, v.Compare(1, 5), 0, "comparision: 1.10 must be greater than 1.5")
		assert.Equal(t, v.Compare(1, 10), 0, "comparision: must be equal")
		assert.Less(t, v.Compare(1, 12), 0, "comparison: 1.10 must be less than 1.12")

		v = version.NewVersion(101, 1000)
		assert.Equal(t, v.Compare(101, 1000), 0, "comparision: must be equal")
		assert.Greater(t, v.Compare(10, 11000), 0, "comparision: 101.1000 must be greater than 10.11000")
		assert.Less(t, v.Compare(1011, 0), 0, "comparision: 101.1000 must be less than 1011.0")
		assert.Greater(t, v.Compare(1, 1011000), 0, "comparision: 101.1000 must be less than 1.1011000")
	})

	invalid := []string{
		"foo",
		"1",
		"a.b",
		"1.a",
		"a.2",
		"-1.0",
		"0.-1",
		"1000000000000000000000000.0",
	}
	valid := map[string]version.Version{
		"10.1":   version.NewVersion(10, 1),
		"2.10.1": version.NewVersion(2, 10),
	}

	t.Run("parsing", func(t *testing.T) {
		for _, str := range invalid {
			t.Run(str, func(t *testing.T) {
				_, err := version.Parse(str)
				assert.Error(t, err)
			})
		}
		for str, expected := range valid {
			t.Run(str, func(t *testing.T) {
				actual, err := version.Parse(str)
				if assert.NoError(t, err) {
					assert.Equal(t, expected, actual)
				}
			})
		}
	})
}
