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

# The test breaks other OLM tests (https://github.com/intel/pmem-csi/issues/1029).
TEST_E2E_SKIP_ALL += olm.*upgrade

# Was too flaky when trying to build v1.0.2.
TEST_E2E_SKIP_ALL += can.publish.volume.after.a.node.driver.restart

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

# Additional e2e.test arguments, like -ginkgo.failFast.
TEST_E2E_ARGS =

empty:=
space:= $(empty) $(empty)

# E2E testing relies on a running QEMU test cluster. It therefore starts it,
# but because it might have been running already and might have to be kept
# running to debug test failures, it doesn't stop it.
# Use count=1 to avoid test results caching, does not make sense for e2e test.
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
	$(GO) test -count=1 -timeout 0 -v ./test/e2e -args \
                -v=5 \
                -ginkgo.skip='$(subst $(space),|,$(strip $(subst @,$(space),$(TEST_E2E_SKIP_ALL))))' \
                -ginkgo.focus='$(subst $(space),|,$(strip $(subst @,$(space),$(TEST_E2E_FOCUS))))' \
		-ginkgo.randomizeAllSpecs=false \
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
_work/gocovmerge-$(GOCOVMERGE_VERSION):
	tmpdir=`mktemp -d` && \
	trap 'rm -r $$tmpdir' EXIT && \
	cd $$tmpdir && \
	echo "module foo" >go.mod && \
	go get github.com/wadey/gocovmerge@$(GOCOVMERGE_VERSION) && \
	go build -o $(abspath $@) github.com/wadey/gocovmerge
	ln -sf $(@F) _work/gocovmerge

# This is a special target that runs unit and E2E testing and
# combines the various cover profiles into one. To re-run testing,
# remove the file or use "make coverage".
#
# We remove all pmem-csi-driver coverage files
# before testing, restart the driver, and then collect all
# files, including the ones written earlier by init containers.
_work/coverage.out: _work/gocovmerge-$(GOCOVMERGE_VERSION)
	$(MAKE) start
	@ echo "removing old pmem-csi-driver coverage information from all nodes"
	@ for ssh in _work/$(CLUSTER)/ssh.*; do for i in $$($$ssh ls /var/lib/pmem-csi-coverage/pmem-csi-driver* 2>/dev/null); do (set -x; $$ssh rm $$i); done; done
	@ rm -rf _work/coverage
	@ mkdir _work/coverage
	@ go clean -testcache
	$(subst go test,go test -coverprofile=$(abspath _work/coverage/unit.out) -covermode=atomic,$(RUN_TESTS))
	$(RUN_E2E)
	@ echo "killing pmem-csi-driver to flush coverage data"
	@ for ssh in _work/$(CLUSTER)/ssh.*; do (set -x; $$ssh killall pmem-csi-driver); done
	@ echo "waiting for all pods to restart"
	@ while _work/$(CLUSTER)/ssh.0 kubectl get --no-headers pods | grep -q -v Running; do sleep 5; done
	@ echo "collecting coverage data"
	@ for ssh in _work/$(CLUSTER)/ssh.*; do for i in $$($$ssh ls /var/lib/pmem-csi-coverage/ 2>/dev/null); do (set -x; $$ssh cat /var/lib/pmem-csi-coverage/$$i) >_work/coverage/$$(echo $$ssh | sed -e 's;.*/ssh\.;;').$$i; done; done
	$< _work/coverage/* >$@

_work/coverage.html: _work/coverage.out
	$(GO) tool cover -html $< -o $@

_work/coverage.txt: _work/coverage.out check-go-version-$(GO_BINARY)
	$(GO) tool cover -func $< -o $@

.PHONY: coverage
coverage:
	@ rm -rf _work/coverage.out
	$(MAKE) _work/coverage.txt _work/coverage.html
