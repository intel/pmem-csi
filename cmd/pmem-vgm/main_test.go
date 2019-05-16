package main_test

import (
	"testing"

	"github.com/intel/pmem-csi/pkg/coverage"
	"github.com/intel/pmem-csi/pkg/pmem-vgm"
)

func TestMain(t *testing.T) {
	coverage.Run(pmemvgm.Main)
}
