/*

Copyright (c) 2017-2019 Intel Corporation

SPDX-License-Identifier: Apache-2.0

*/

// Package test contains a black-box test for the imagefile package.
// It is a separate package to allow importing it into the E2E suite
// where we can use the setup files for the current cluster to
// actually use the image file inside a VM.
package test

import (
	"testing"
)

type TWrapper testing.T

func (t *TWrapper) Outer(name string, cb func(t TInterface)) {
	(*testing.T)(t).Run(name, func(t *testing.T) {
		cb((*TWrapper)(t))
	})
}

func (t *TWrapper) Inner(name string, cb func(t TInterface)) {
	t.Outer(name, cb)
}

func (t *TWrapper) Parallel() {}

// TestImageFile is used by "go test".
func TestImageFile(t *testing.T) {
	ImageFile((*TWrapper)(t))
}
