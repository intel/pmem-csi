/*
Copyright 2022 Intel Corporation

SPDX-License-Identifier: Apache-2.0
*/

package xfs

import (
	"testing"
)

func Test_ConfigureFS(t *testing.T) {
	// This is assumed to be backed by tmpfs and thus doesn't support xattr.
	tmp := t.TempDir()
	err := ConfigureFS(tmp)
	if err == nil {
		t.Fatal("did not get expected error")
	}
	t.Logf("got expected error: %v", err)
}
