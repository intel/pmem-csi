package pmemnsinit_test

import (
	"testing"

	"github.com/intel/pmem-csi/pkg/pmem-ns-init"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestPmemNSInit(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NSInit Suite")
}

/*
var _ = BeforeSuite(func() {
	var err error
	Expect(err).NotTo(HaveOccurred())
})

var _ = AfterSuite(func() {
})*/

var _ = Describe("pmem-ns-init", func() {
	Context("Check arguments", func() {
		type cases struct {
			name        string
			useforfsdax int
		}
		goodcases := []cases{
			{"useforfsdax below 100", 50},
			{"useforfsdax 100", 100},
		}
		badcases := []cases{
			{"useforfsdax negative", -1},
			{"useforfsdax too large", 101},
		}

		for _, c := range goodcases {
			c := c
			It(c.name, func() {
				err := pmemnsinit.CheckArgs(c.useforfsdax)
				Expect(err).NotTo(HaveOccurred())
			})
		}
		for _, c := range badcases {
			c := c
			It(c.name, func() {
				err := pmemnsinit.CheckArgs(c.useforfsdax)
				Expect(err).To(HaveOccurred())
			})
		}
	})
})
