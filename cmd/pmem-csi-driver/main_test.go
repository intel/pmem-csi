package main_test

import (
	"testing"

	"github.com/intel/pmem-csi/pkg/coverage"
	pmemcsidriver "github.com/intel/pmem-csi/pkg/pmem-csi-driver"
)

func TestMain(t *testing.T) {
	coverage.Run(pmemcsidriver.Main)
}
