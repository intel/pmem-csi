OPERATOR_SDK_VERSION=1.15.0
CONTROLLER_GEN_VERSION=v0.7.0
CONTROLLER_GEN=_work/bin/controller-gen-$(CONTROLLER_GEN_VERSION)

# download operator-sdk binary
_work/bin/operator-sdk-$(OPERATOR_SDK_VERSION):
	mkdir -p $(@D)
	curl -L https://github.com/operator-framework/operator-sdk/releases/download/v$(OPERATOR_SDK_VERSION)/operator-sdk_linux_amd64 -o $@
	chmod a+x $@
	ln -sf $(@F) $(@D)/operator-sdk

# Re-generates the K8S source. This target is supposed to run
# upon any changes made to operator api.
operator-generate-k8s: $(CONTROLLER_GEN)
	$< object paths=./pkg/apis/...

# Build controller-gen from source.
$(CONTROLLER_GEN):
	mkdir -p $(@D)
	GOBIN=$(abspath $(@D)) $(GO) install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)
	ln -sf controller-gen $@


MANIFESTS_DIR=deploy/kustomize/olm-catalog
CATALOG_DIR=deploy/olm-catalog
BUNDLE_DIR=deploy/olm-bundle/$(MAJOR_MINOR_PATCH_VERSION)
CRD_DIR=deploy/crd
# CHANNELS: A comma-separated list of channels the generated
# bundle belongs to. This supposed to be all the channel names we support.
# Currently, alpha(all < v1.0) and stable(=v1.0)
CHANNELS?=alpha,stable
# DEFAULT_CHANNEL: The default channel for the generated bundle.
DEFAULT_CHANNEL?=stable
# The OpenShift versions we are compatible with.
# A single version means that version and later.
# See https://redhat-connect.gitbook.io/certified-operator-guide/ocp-deployment/operator-metadata/bundle-directory/managing-openshift-versions
# 4.6 = Kubernetes 1.19
# 4.7 = Kubernetes 1.20
# 4.8 = Kubernetes 1.21
OPENSHIFT_VERSIONS?=v4.6

PATCH_VERSIONS := sed -i -e 's;X\.Y\.Z;$(MAJOR_MINOR_PATCH_VERSION);g' -e 's;X\.Y;$(MAJOR_MINOR_VERSION);g'
PATCH_REPLACES := sed -i -e 's;\(.*\)\(version:.*\);\1replaces: pmem-csi-operator.v$(REPLACES)\n\1\2;g'
PATCH_DATE := sed -i -e 's;\(.*createdAt: \).*;\1$(shell date +%FT%TZ);g'

# Generate CRD
operator-generate-crd: $(CONTROLLER_GEN)
	@echo "Generating CRD in $(CRD_DIR) ..."
	$(CONTROLLER_GEN) crd paths=./pkg/apis/... output:dir=$(CRD_DIR)
	sed -i "1s/^/# This file was generated by controller-gen $(CONTROLLER_GEN_VERSION) via 'make operator-generate-crd'\n/" $(CRD_DIR)/*

# Generate bundle manifests for OLM
# The bundle.Dockerfile file is generated in the $root folder,
# and the file paths in the generated dockerfile is related to
# root directory.
# - We move the dockerfile into version specified bundle directory.
# - And adjust the filepaths.
# - Remove reference to 'scorecard' as we do not have any scorecard
#   test configurations.
operator-generate-bundle: _work/bin/operator-sdk-$(OPERATOR_SDK_VERSION) _work/kustomize operator-generate-crd
	@echo "Generating operator bundle in $(BUNDLE_DIR) ..."
	rm -rf $(BUNDLE_DIR)
	mkdir -p $(BUNDLE_DIR)
	# Generate input YAML first. This might fail...
	_work/kustomize build --load-restrictor LoadRestrictionsNone $(MANIFESTS_DIR) >$(BUNDLE_DIR)/manifests.yaml
	# Specifying --kustomize-dir seems redundant because stdin already contains our ClusterServiceVersion,
	# but without it the final ClusterServiceVersion in the bundle just has some automatically generated fields.
	cat $(BUNDLE_DIR)/manifests.yaml | $< generate bundle --kustomize-dir=$(MANIFESTS_DIR) --version=$(MAJOR_MINOR_PATCH_VERSION) --output-dir=$(BUNDLE_DIR) --channels ${CHANNELS} --default-channel ${DEFAULT_CHANNEL}
	rm $(BUNDLE_DIR)/manifests.yaml
	$(PATCH_VERSIONS) $(BUNDLE_DIR)/manifests/pmem-csi-operator.clusterserviceversion.yaml
ifdef REPLACES
	$(PATCH_REPLACES) $(BUNDLE_DIR)/manifests/pmem-csi-operator.clusterserviceversion.yaml
endif
	$(PATCH_DATE) $(BUNDLE_DIR)/manifests/pmem-csi-operator.clusterserviceversion.yaml
	sed -i -e "s;$(BUNDLE_DIR)/;;g"  -e "/scorecard/d" -e '/FROM scratch/a LABEL com.redhat.openshift.versions="$(OPENSHIFT_VERSIONS)"' bundle.Dockerfile
	sed -i -e "/scorecard/d" $(BUNDLE_DIR)/metadata/annotations.yaml
	mv bundle.Dockerfile $(BUNDLE_DIR)
	@make operator-validate-bundle

operator-validate-bundle: _work/bin/operator-sdk-$(OPERATOR_SDK_VERSION) $(BUNDLE_DIR)
	@if ! OUT="$$($< bundle validate --select-optional  name=operatorhub $(BUNDLE_DIR))"; then \
		echo >&2 "ERROR: Operator bundle in $(BUNDLE_DIR) did not pass validation:"; \
		echo >&2 "$$OUT"; \
		exit 1; \
	fi

.PHONY: operator-generate
operator-generate: operator-generate-crd operator-generate-bundle

operator-clean-crd:
	rm -rf $(CRD_DIR)

operator-clean-bundle:
	rm -rf $(BUNDLE_DIR)

test: test-operator-generate
.PHONY: test-operator-generate
test-operator-generate: operator-generate
	if ! git diff deploy; then \
		echo >&2 "ERROR: deploy directory content was modified by `make operator-generate`"; \
	fi
