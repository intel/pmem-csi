package main

import (
	"flag"
	"os"

	"github.com/intel/pmem-csi/pkg/pmem-vgm"
)

func main() {
	flag.Parse()
	os.Exit(pmemvgm.Main())
}
