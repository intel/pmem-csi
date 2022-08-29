TEST_CMD=$(GO) test
TEST_ARGS=$(IMPORT_PATH)/pkg/...
TEST_PKGS=$(shell $(GO) list $(TEST_ARGS) | sed -e 's;$(IMPORT_PATH);.;')

.PHONY: vet
test: vet check-go-version-$(GO_BINARY)
	$(GO) vet $(IMPORT_PATH)/pkg/...

# Check resp. fix formatting.
.PHONY: test_fmt fmt
test: test_fmt
test_fmt:
	@ files=$$(find pkg cmd test -name '*.go'); \
	if [ $$(gofmt -d $$files | wc -l) -ne 0 ]; then \
		echo "formatting errors:"; \
		gofmt -d $$files; \
		false; \
	fi

fmt:
	gofmt -l -w $$(find pkg cmd -name '*.go')

# Verify that the go.mod is up-to-date and clean.
.PHONY: test_vendor
test: test_vendor
test_vendor:
	hack/verify-vendor.sh

# Verify that generated code is up-to-date.
.PHONY: test_generated
test: test_generated
test_generated:
	hack/verify-generated.sh

# This ensures that we know about all components that are needed at
# runtime on a production system. Those must be scrutinized more
# closely than components that are merely needed for testing.
#
# Intel has a process for this. The mapping from import path to "name"
# + "download URL" must match how the components are identified at
# Intel while reviewing the components.
.PHONY: test_runtime_deps
test: test_runtime_deps

GO_LICENSES = _work/bin/go-licenses

test_runtime_deps: $(GO_LICENSES)
	@ if ! diff -c \
		runtime-deps.csv \
		<( $(RUNTIME_DEPS) ); then \
		echo; \
		echo "runtime-deps.csv not up-to-date. Review and finally apply the patch above."; \
		false; \
	fi

# Because of the dependency on the version check, the command will get
# installed anew for each invocation. This is desirable because then
# we really do use the latest version. We cannot lock to a specific one
# because the go-licenses repo has no tags.
$(GO_LICENSES): check-go-version-$(GO_BINARY)
	mkdir -p $(@D)
	GOBIN=$(abspath $(@D)) $(GO) install github.com/google/go-licenses@latest

# We are using https://github.com/google/go-licenses to track all dependencies of our production binaries.
RUNTIME_DEPS = $(GO_LICENSES) csv ./cmd/...

# We don't care about the URL to the source code (second column in output),
# therefore we can ignore all errors related to that.
RUNTIME_DEPS += 2> >( grep -v -e 'Error discovering URL for' -e 'unsupported package host' -e 'remote not found' -e 'the Git remote .* does not have a valid URL' -e 'cannot determine URL for .* package' >&2 )

# We simply throw away the second colume with the URL.
RUNTIME_DEPS += | sed -e 's/\([^,]*\),[^,]*/\1/'

# We ignore our own source code.
RUNTIME_DEPS += | grep -v ^github.com/intel/pmem-csi

# Ignore duplicates and sort.
RUNTIME_DEPS += | env LC_ALL=C LANG=C sort -u

# Execute simple unit tests.
#
# pmem-device-manager gets excluded because its tests need a special
# environment. We could run it, but its tests would just be skipped
# (https://github.com/intel/pmem-csi/pull/420#discussion_r346850741).
.PHONY: run_tests
test: run_tests
RUN_TESTS = TEST_WORK=$(abspath _work) REPO_ROOT=`pwd` \
	env GODEBUG=x509ignoreCN=0 $(TEST_CMD) -timeout 0 $(filter-out %/pmem-device-manager %/imagefile/test,$(TEST_PKGS))
RUN_TEST_DEPS = check-go-version-$(GO_BINARY)

run_tests: $(RUN_TEST_DEPS)
	$(RUN_TESTS)

# E2E tests which are known to be unsuitable (space or @ separated list of regular expressions).
TEST_E2E_SKIP =
TEST_E2E_SKIP_ALL = $(TEST_E2E_SKIP)

# Depends on test/e2e/testing-manifests/storage-csi/any-volume-datasource/crd/populator.storage.k8s.io_volumepopulators.yaml
# which isn't available when we import the test suite.
TEST_E2E_SKIP_ALL += provisioning.should.provision.storage.with.any.volume.data.source

# The test's check whether a driver supports multiple nodes is incomplete and does
# not work for the topology-based single-node access in PMEM-CSI:
# https://github.com/kubernetes/kubernetes/blob/25ffbe633810609743944edd42d164cd7990071c/test/e2e/storage/testsuites/provisioning.go#L175-L181
TEST_E2E_SKIP_ALL += should.access.volume.from.different.nodes

# Test is flawed and will become optional soon (probably csi-test 3.2.0): https://github.com/kubernetes-csi/csi-test/pull/258
TEST_E2E_SKIP_ALL += NodeUnpublishVolume.*should.fail.when.the.volume.is.missing

# Test asks for too small volume and then fails to write the complete file (https://github.com/kubernetes/kubernetes/issues/103718).
TEST_E2E_SKIP_ALL += volumeIO.*should.write.files.of.various.sizes.*verify.size.*validate.content

# Do not run driver stress tests in direct mode, they are consuming more time(~17m per test)
# The reason is shredding the ndctl device is consuming most of the time.
TEST_E2E_SKIP_ALL += direct.*binding.stress.test

# This test relies on new behavior in kubelet 1.24.
# https://github.com/kubernetes/kubernetes/pull/107065/commits/4a076578451aa27e8ac60beec1fd3f23918c5331#r855413674
TEST_E2E_SKIP_1.23 += should.mount.multiple.PV.pointing.to.the.same.storage.on.the.same.node
TEST_E2E_SKIP_1.22 += should.mount.multiple.PV.pointing.to.the.same.storage.on.the.same.node
TEST_E2E_SKIP_1.21 += should.mount.multiple.PV.pointing.to.the.same.storage.on.the.same.node

# These tests depend on ephemeral containers, a feature only enabled by default in Kubernetes 1.25.
TEST_E2E_SKIP_1.21 += volumes.should.store.data
TEST_E2E_SKIP_1.22 += volumes.should.store.data
TEST_E2E_SKIP_1.23 += volumes.should.store.data
TEST_E2E_SKIP_1.24 += volumes.should.store.data

# Fails for Kubernetes <= 1.22 with an incorrect error (fixed later in Kubernetes 1.23):
# Invalid value: "my-volume-0": can only use volume source type of PersistentVolumeClaim for block mode
TEST_E2E_SKIP_1.22 += Generic.Ephemeral-volume..block.volmode
TEST_E2E_SKIP_1.21 += Generic.Ephemeral-volume..block.volmode

# Add all Kubernetes version-specific suppressions.
TEST_E2E_SKIP_ALL += $(TEST_E2E_SKIP_$(shell cat _work/$(CLUSTER)/kubernetes.version))

# E2E tests which are to be executed (space separated list of regular expressions, default is all that aren't skipped).
TEST_E2E_FOCUS =

foobar:
	echo TEST_E2E_SKIP_$(shell cat _work/$(CLUSTER)/kubernetes.version)
	echo $(TEST_E2E_SKIP_$(shell cat _work/$(CLUSTER)/kubernetes.version))
	echo $(TEST_E2E_SKIP_ALL)

# E2E Junit output directory (default empty = none). junit_<ginkgo node>.xml files will be written there,
# i.e. usually just junit_01.xml.
TEST_E2E_REPORT_DIR=

# The value for the -ginkgo.timeout parameter.
TEST_E2E_TIMEOUT=5h

# Additional e2e.test arguments, like -ginkgo.failFast.
TEST_E2E_ARGS =

empty:=
space:= $(empty) $(empty)

GO_TEST_E2E = $(GO) test -count=1 -timeout 0 -v ./test/e2e -args
ifneq ($(WITH_DLV),)
GO_TEST_E2E = dlv test ./test/e2e --
endif

# E2E testing relies on a running QEMU test cluster. It therefore starts it,
# but because it might have been running already and might have to be kept
# running to debug test failures, it doesn't stop it.
# Use count=1 to avoid test results caching, does not make sense for e2e test.
#
# -e2e-verify-service-account might be something that only works for Kubernetes
# >= 1.24 - see
# https://github.com/kubernetes/kubernetes/issues/108307#issuecomment-1074394385
# https://github.com/kubernetes/kubernetes/pull/107763
.PHONY: test_e2e
RUN_E2E = KUBECONFIG=`pwd`/_work/$(CLUSTER)/kube.config \
	REPO_ROOT=`pwd` \
	CLUSTER=$(CLUSTER) \
	$(shell source test/test-config.sh; \
	  echo TEST_KUBERNETES_VERSION=$$TEST_KUBERNETES_VERSION; \
	  echo PMEM_CSI_IMAGE=$$TEST_LOCAL_REGISTRY/pmem-csi-driver:$(IMAGE_VERSION); \
	) \
	TEST_CMD='$(TEST_CMD)' \
	GO='$(GO)' \
	TEST_PKGS='$(shell for i in ./pkg/pmem-device-manager ./pkg/imagefile/test; do if ls $$i/*_test.go 2>/dev/null >&2; then echo $$i; fi; done)' \
	$(GO_TEST_E2E) \
                -v=5 \
                -e2e-verify-service-account=false \
                -ginkgo.skip='$(subst $(space),|,$(strip $(subst @,$(space),$(TEST_E2E_SKIP_ALL))))' \
                -ginkgo.focus='$(subst $(space),|,$(strip $(subst @,$(space),$(TEST_E2E_FOCUS))))' \
		-ginkgo.randomizeAllSpecs=false \
		-ginkgo.timeout=$(TEST_E2E_TIMEOUT) \
	        $(TEST_E2E_ARGS) \
                -report-dir=$(TEST_E2E_REPORT_DIR)
test_e2e: start $(RUN_TEST_DEPS) operator-generate-bundle
	$(RUN_E2E)

run_dm_tests: TEST_BINARY_NAME=pmem-dm-tests
run_dm_tests: NODE=pmem-csi-$(CLUSTER)-worker1
run_dm_tests: _work/bin/govm start_test_vm
	$(TEST_CMD) ./pkg/pmem-device-manager -v -c -o $(PWD)/_work/$(shell echo  $(TEST_BINARY_NAME))
	NODE_IP=$(shell `pwd`/_work/bin/govm list -f '{{select (filterRegexp . "Name" "^'$(NODE)'") "IP"}}'); \
	SSH=$$(grep -l $$NODE_IP _work/$(CLUSTER)/ssh.*); \
	SSH_USER=$$(grep ^exec $$SSH | rev | cut -f2 -d' ' | rev | cut -f1 -d'@'); \
	SSH_ARGS=$$(grep ^exec $$SSH | cut -f3- -d' ' | rev | cut -f3- -d' ' | rev); \
	scp $$SSH_ARGS `pwd`/_work/$(TEST_BINARY_NAME) $$SSH_USER@$$NODE_IP:. ; \
	$$SSH sudo ./$(TEST_BINARY_NAME) -ginkgo.v

_work/%/.ca-stamp: test/setup-ca.sh _work/.setupcfssl-stamp
	rm -rf $(@D)
	WORKDIR='$(@D)' PATH='$(PWD)/_work/bin/:$(PATH)' NS=default $<
	touch $@


_work/.setupcfssl-stamp: CFSSL_VERSION=1.5.0
_work/.setupcfssl-stamp:
	rm -rf _work/bin
	curl -L https://github.com/cloudflare/cfssl/releases/download/v$(CFSSL_VERSION)/cfssl_$(CFSSL_VERSION)_linux_amd64 -o _work/bin/cfssl --create-dirs
	curl -L https://github.com/cloudflare/cfssl/releases/download/v$(CFSSL_VERSION)/cfssljson_$(CFSSL_VERSION)_linux_amd64 -o _work/bin/cfssljson --create-dirs

	chmod a+x _work/bin/cfssl _work/bin/cfssljson
	touch $@

# Build gocovmerge at a certain revision. Depends on go >= 1.11
# because we use module support.
GOCOVMERGE_VERSION=b5bfa59ec0adc420475f97f89b58045c721d761c
.PHONY: _work/gocovmerge
_work/gocovmerge: _work/gocovmerge-$(GOCOVMERGE_VERSION)
_work/gocovmerge-$(GOCOVMERGE_VERSION): check-go-version-$(GO_BINARY)
	GOBIN=`pwd`/_work go install github.com/wadey/gocovmerge@$(GOCOVMERGE_VERSION)
	ln -sf gocovmerge  $@

GOCOVER_COBERTURA_VERSION=aaee18c8195c3f2d90e5ef80ca918d265463842a
.PHONY: _work/gocover-cobertura
_work/gocover-cobertura: _work/gocover-cobertura-$(GOCOVER_COBERTURA_VERSION)
_work/gocover-cobertura-$(GOCOVER_COBERTURA_VERSION): check-go-version-$(GO_BINARY)
	GOBIN=`pwd`/_work go install github.com/t-yuki/gocover-cobertura@$(GOCOVER_COBERTURA_VERSION)
	ln -sf gocover-cobertura $@

# This is a special target that collects the coverage information from E2E
# testing (which includes Go unit tests) and combines it into one file.
#
# Beware that "make kustomize KUSTOMIZE_WITH_COVERAGE=true" must have been run
# before starting tests. This replaces YAML files in "deploy" with versions
# that support coverage.
#
# To ensure that the results of the current driver instance are included, it
# gets removed first.
.PHONY: _work/coverage.out
_work/coverage/coverage.out: _work/gocovmerge-$(GOCOVMERGE_VERSION)
	@ echo "removing PMEM-CSI to flush coverage data"
	test/delete-deployment.sh
	@ echo "collecting coverage data"
	@ rm -rf ${@D}
	@ mkdir -p ${@D}
	@ node=0; while true; do ssh=_work/$(CLUSTER)/ssh.$$node; if ! [ -e $$ssh ]; then break; fi; for i in $$($$ssh ls /var/lib/pmem-csi-coverage/ 2>/dev/null); do in=/var/lib/pmem-csi-coverage/$$i; out=${@D}/node-$$node.$$i; (set -x; $$ssh sudo cat $$in | tee $$out >/dev/null); done; node=$$((node + 1)); done
	$< ${@D}/* >$@

_work/coverage/coverage.html: _work/coverage/coverage.out check-go-version-$(GO_BINARY)
	$(GO) tool cover -html $< -o $@

_work/coverage/coverage.txt: _work/coverage/coverage.out check-go-version-$(GO_BINARY)
	$(GO) tool cover -func $< -o $@

# Convert to Cobertura XML for Jenkins.
_work/coverage/coverage.xml: _work/gocover-cobertura-$(GOCOVER_COBERTURA_VERSION) _work/coverage/coverage.out
	$<  <_work/coverage/coverage.out >$@

# This runs all steps for coverage profile collection.
# The Jenkinsfile has its own rules for this.
.PHONY: coverage
coverage:
	@ rm -rf _work/coverage
	$(MAKE) kustomize KUSTOMIZE_WITH_COVERAGE=true"
	$(MAKE) start
	@ echo "removing old PMEM-CSI installation"
	test/delete-deployment.sh
	@ echo "removing old pmem-csi-driver coverage information from all nodes"
	@ for ssh in _work/$(CLUSTER)/ssh.*; do $$ssh sudo rm -f /var/lib/pmem-csi-coverage/pmem-csi-driver* || exit 1; done
	$(RUN_E2E)
	$(MAKE) _work/coverage.txt _work/coverage.html
