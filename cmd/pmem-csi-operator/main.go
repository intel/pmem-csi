package main

import (
	"flag"
	"os"

	"github.com/intel/pmem-csi/pkg/pmem-csi-operator"
)

func main() {
	flag.Parse()
	os.Exit(pmemoperator.Main())
}
