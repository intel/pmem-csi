package main

import (
	"os"

	"github.com/intel/pmem-csi/pkg/pmem-ns-init"
)

func main() {
	os.Exit(pmemnsinit.Main())
}
