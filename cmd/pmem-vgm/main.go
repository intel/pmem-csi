package main

import (
	"os"

	"github.com/intel/pmem-csi/pkg/pmem-vgm"
)

func main() {
	os.Exit(pmemvgm.Main())
}
