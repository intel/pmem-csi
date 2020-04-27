/*
Copyright 2020 Intel Coporation.

SPDX-License-Identifier: Apache-2.0
*/

// Package tooling contains dependencies for some of the code that
// we only need at build time. It ensures that "go mod tidy" doesn't remove
// those dependencies from the top-level go.mod.
package tooling

import (
	_ "github.com/go-bindata/go-bindata"
)
