.PHONY: pmem-cis-operator
pmem-csi-operator: check-go-version-$(GO_BINARY)
	$(GO) build -ldflags '-X github.com/intel/pmem-csi/pkg/$@.version=${VERSION}' -a -o ${OUTPUT_DIR}/$@ ./operator/cmd/manager