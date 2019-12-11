OPERATOR_SDK_VERSION=v0.12.0

# download operator-sdk binary
_work/bin/operator-sdk-$(OPERATOR_SDK_VERSION):
	mkdir -p _work/bin/ 2> /dev/null
	curl -L https://github.com/operator-framework/operator-sdk/releases/download/$(OPERATOR_SDK_VERSION)/operator-sdk-$(OPERATOR_SDK_VERSION)-x86_64-linux-gnu -o $(abspath $@)
	chmod a+x $(abspath $@)

# Re-generates the K8S source, this target is supposed to run
# upon any changes made to operator api
#
# operator-sdk treats operator source is a self-contained go module, this is not
# true in our case. So as a work around we copy/link parent go.{mod,sum} files
# to operator folder.
operator-generate-k8s: _work/bin/operator-sdk-$(OPERATOR_SDK_VERSION)
	cd ./operator && ( \
		ln -s ../go.mod && \
		ln -s ../go.sum && \
		../_work/bin/operator-sdk-$(OPERATOR_SDK_VERSION) generate k8s; \
		rm ./go.mod ./go.sum; \
	)

.PHONY: pmem-cis-operator
pmem-csi-operator: check-go-version-$(GO_BINARY)
	$(GO) build -ldflags '-X github.com/intel/pmem-csi/pkg/$@.version=${VERSION}' -a -o ${OUTPUT_DIR}/$@ ./operator/cmd/manager
