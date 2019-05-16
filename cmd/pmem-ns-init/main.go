package main

import (
	"flag"
	"os"

	"github.com/intel/pmem-csi/pkg/pmem-ns-init"
)

func main() {
	flag.Parse()
	os.Exit(pmemnsinit.Main())
}
