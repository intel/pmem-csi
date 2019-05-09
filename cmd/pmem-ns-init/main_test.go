package main_test

import (
	"testing"

	"github.com/intel/pmem-csi/pkg/coverage"
	"github.com/intel/pmem-csi/pkg/pmem-ns-init"
)

func TestMain(t *testing.T) {
	coverage.Run(pmemnsinit.Main)
}
