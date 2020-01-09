package main_test

import (
	"testing"

	"github.com/intel/pmem-csi/pkg/coverage"
	pmemoperator "github.com/intel/pmem-csi/pkg/pmem-csi-operator"
)

func TestMain(t *testing.T) {
	coverage.Run(pmemoperator.Main)
}
