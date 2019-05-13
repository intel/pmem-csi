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
			name                      string
			useforfsdax, useforsector int
		}
		goodcases := []cases{
			{"useforfsdax ok", 50, 0},
			{"useforsector ok", 0, 50},
			{"useforfsdax and useforsector combined 100", 70, 30},
			{"useforfsdax and useforsector combined less than 100", 40, 30},
		}
		badcases := []cases{
			{"useforfsdax negative", -1, 0},
			{"useforsector negative", 0, -1},
			{"useforfsdax too large", 101, 0},
			{"useforsector too large", 0, 101},
			{"useforfsdax and useforsector combined too large", 51, 51},
		}

		for _, c := range goodcases {
			c := c
			It(c.name, func() {
				err := pmemnsinit.CheckArgs(c.useforfsdax, c.useforsector)
				Expect(err).NotTo(HaveOccurred())
			})
		}
		for _, c := range badcases {
			c := c
			It(c.name, func() {
				err := pmemnsinit.CheckArgs(c.useforfsdax, c.useforsector)
				Expect(err).To(HaveOccurred())
			})
		}
	})
})
