/*

Copyright (c) 2017-2019 Intel Corporation

SPDX-License-Identifier: Apache-2.0

*/

package imagefilee2e

import (
	"fmt"

	"github.com/onsi/ginkgo"

	"github.com/intel/pmem-csi/pkg/imagefile/test"
	"github.com/intel/pmem-csi/test/e2e/deploy"
)

type tImplementation struct {
	ginkgo.GinkgoTInterface
}

func (t *tImplementation) Outer(name string, cb func(t test.TInterface)) {
	ginkgo.Context(name, func() {
		cb(t)
	})
}

func (t *tImplementation) Inner(name string, cb func(t test.TInterface)) {
	ginkgo.It(name, func() {
		// This code now can call GinkgoT and pass some actual implementation
		// of the interface.
		cb(&tImplementation{ginkgo.GinkgoT()})
	})
}

// Only necessary because of https://github.com/onsi/ginkgo/issues/659
func (t *tImplementation) Skipf(format string, args ...interface{}) {
	ginkgo.Skip(fmt.Sprintf(format, args...))
}

var _ = deploy.Describe("", "imagefile", "", func(d *deploy.Deployment) {
	// Our Outer and Inner implementation do not need a valid pointer.
	test.ImageFile((*tImplementation)(nil))
})
