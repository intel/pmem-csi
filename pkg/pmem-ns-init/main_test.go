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
			name                                     string
			namespacesize, useforfsdax, useforsector int
		}
		goodcases := []cases{
			{"namespacesize ok", 32, 0, 0},
			{"useforfsdax ok", 32, 50, 0},
			{"useforsector ok", 32, 0, 50},
			{"useforfsdax and useforsector combined 100", 32, 70, 30},
			{"useforfsdax and useforsector combined less than 100", 32, 40, 30},
		}
		badcases := []cases{
			{"namespacesize negative", -1, 0, 0},
			{"namespacesize too small", 1, 0, 0},
			{"useforfsdax negative", 32, -1, 0},
			{"useforsector negative", 32, 0, -1},
			{"useforfsdax too large", 32, 101, 0},
			{"useforsector too large", 32, 0, 101},
			{"useforfsdax and useforsector combined too large", 32, 51, 51},
		}

		for _, c := range goodcases {
			c := c
			It(c.name, func() {
				err := pmemnsinit.CheckArgs(c.namespacesize, c.useforfsdax, c.useforsector)
				Expect(err).NotTo(HaveOccurred())
			})
		}
		for _, c := range badcases {
			c := c
			It(c.name, func() {
				err := pmemnsinit.CheckArgs(c.namespacesize, c.useforfsdax, c.useforsector)
				Expect(err).To(HaveOccurred())
			})
		}
	})
})
