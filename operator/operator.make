OPERATOR_SDK_VERSION=v1.0.0

# download operator-sdk binary
_work/bin/operator-sdk-$(OPERATOR_SDK_VERSION):
	mkdir -p _work/bin/ 2> /dev/null
	curl -L https://github.com/operator-framework/operator-sdk/releases/download/$(OPERATOR_SDK_VERSION)/operator-sdk-$(OPERATOR_SDK_VERSION)-x86_64-linux-gnu -o $(abspath $@)
	chmod a+x $(abspath $@)

# Re-generates the K8S source. This target is supposed to run
# upon any changes made to operator api.
#
# GOROOT is needed because of https://github.com/operator-framework/operator-sdk/issues/1854#issuecomment-525132306
operator-generate-k8s: _work/bin/operator-sdk-$(OPERATOR_SDK_VERSION)
	GOROOT=$(shell $(GO) env GOROOT) _work/bin/operator-sdk-$(OPERATOR_SDK_VERSION) generate k8s

# find or download if necessary controller-gen
# this make target is copied from Makefile generated
# by operator-sdk init
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
	set -e; \
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	GOPATH=$$($(GO) env GOPATH) ;\
	$(GO) mod init tmp ;\
	$(GO) get sigs.k8s.io/controller-tools/cmd/controller-gen@v0.3.0 ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR; \
	}
CONTROLLER_GEN=$(GOPATH)/bin/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

MANIFESTS_DIR=deploy/kustomize/olm-catalog
CATALOG_DIR=deploy/olm-catalog
BUNDLE_DIR=deploy/bundle
SHORT_VERSION=$(shell echo $(VERSION) | cut -f1,2 -d'.')

# Generate CRD and add kustomization support
operator-generate-crd: controller-gen
	@echo "Generating CRD ..."
	$(CONTROLLER_GEN) crd:trivialVersions=true,crdVersions=v1beta1 paths=./pkg/apis/... output:dir=$(MANIFESTS_DIR)/crd/
	@echo "resources: [pmem-csi.intel.com_deployments.yaml]" > $(MANIFESTS_DIR)/crd/kustomization.yaml

# Generate packagemanifests using operator-sdk.
operator-generate-catalog: _work/bin/operator-sdk-$(OPERATOR_SDK_VERSION) _work/kustomize operator-generate-crd
	@echo "Generating base catalog ..."
	@_work/kustomize build --load_restrictor=none $(MANIFESTS_DIR) | $< generate packagemanifests --version $(VERSION) \
		--kustomize-dir $(MANIFESTS_DIR) --output-dir $(CATALOG_DIR)
	@sed -i -e 's;X.Y.Z;$(VERSION);g' -e 's;X.Y;$(SHORT_VERSION);g' $(CATALOG_DIR)/$(VERSION)/pmem-csi-operator.clusterserviceversion.yaml
	$(MAKE) operator-clean-crd

operator-generate-bundle: _work/bin/operator-sdk-$(OPERATOR_SDK_VERSION) _work/kustomize operator-generate-crd
	@echo "Generating operator bundle ..."
	@_work/kustomize build --load_restrictor=none $(MANIFESTS_DIR) | $< generate bundle  --version=$(VERSION) \
        --kustomize-dir=$(MANIFESTS_DIR) --output-dir=$(BUNDLE_DIR)
	@sed -i -e 's;X.Y.Z;$(VERSION);g' -e 's;X.Y;$(SHORT_VERSION);g' $(CATALOG_DIR)/$(VERSION)/pmem-csi-operator.clusterserviceversion.yaml

operator-clean-crd:
	rm -rf $(MANIFESTS_DIR)/crd

operator-clean-catalog:
	rm -rf $(CATALOG_DIR)
