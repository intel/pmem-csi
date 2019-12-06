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


# This ensures that the vendor directory and vendor-bom.csv are in
# sync.  We track components by their license file. Compared to
# focusing on components as seen by Go, this has the advantage that we
# also find and track components with a separate license that are
# embedded in other components (example:
# github.com/onsi/ginkgo/reporters/stenographer/support/go-colorable).
# The downside is that we might miss components with a missing license.
# This has to be caught by code reviews.
.PHONY: test_vendor_bom
test: test_vendor_bom
test_vendor_bom:
	@ if ! diff -c \
		<(tail -n +2 vendor-bom.csv | sed -e 's/;.*//') \
		<(find vendor -name 'LICENSE*' -o -name COPYING | xargs --max-args 1 dirname | sed -e 's;^vendor/;;' | LC_ALL=C LANG=C sort -u); then \
		echo; \
		echo "vendor-bom.csv not in sync with vendor directory:"; \
		echo "+ new entry, missing in vendor-bom.csv"; \
		echo "- obsolete entry in vendor-bom.csv"; \
		false; \
	fi

# Verify that the go.mod is up-to-date and clean and that the "vendor"
# directory contains the matching source code.
.PHONY: test_vendor
test: test_vendor
test_vendor:
	hack/verify-vendor.sh


# This ensures that we know about all components that are needed at
# runtime on a production system. Those must be scrutinized more
# closely than components that are merely needed for testing.
#
# Intel has a process for this. The mapping from import path to "name"
# + "download URL" must match how the components are identified at
# Intel while reviewing the components.
.PHONY: test_runtime_deps
test: test_runtime_deps

test_runtime_deps: check-go-version-$(GO_BINARY)
	@ if ! diff -c \
		runtime-deps.csv \
		<( $(RUNTIME_DEPS) ); then \
		echo; \
		echo "runtime-deps.csv not up-to-date. Update RUNTIME_DEPS in test/test.make, rerun, review and finally apply the patch above."; \
		false; \
	fi

RUNTIME_DEPS =

# We use "go list" because it is readily available. A good replacement
# would be godeps. We list dependencies recursively, not just the
# direct dependencies.
# Filter out the go standard runtime packages from dependecies
RUNTIME_DEPS += diff <($(GO) list -f '{{join .Deps "\n"}}' ./cmd/pmem-csi-driver/ | grep -v ^github.com/intel/pmem-csi | sort -u) \
                <(go list std | sort -u) | grep ^'<' | cut -f2- -d' ' |


# Filter out some packages that aren't really code.
RUNTIME_DEPS += grep -v -e 'github.com/container-storage-interface/spec' |
RUNTIME_DEPS += grep -v -e 'google.golang.org/genproto/googleapis/rpc/status' |

# Reduce the package import paths to project names + download URL.
# - strip prefix
RUNTIME_DEPS += sed -e 's;github.com/intel/pmem-csi/vendor/;;' |
# - use path inside github.com as project name
RUNTIME_DEPS += sed -e 's;^github.com/\([^/]*\)/\([^/]*\).*;github.com/\1/\2;' |
# - everything from gRPC is one project
RUNTIME_DEPS += sed -e 's;google.golang.org/grpc/*.*;grpc-go,https://github.com/grpc/grpc-go;' |
# - various other projects
RUNTIME_DEPS += sed \
	-e 's;github.com/google/uuid;google uuid,https://github.com/google/uuid;' \
	-e 's;github.com/golang/protobuf;golang-protobuf,https://github.com/golang/protobuf;' \
	-e 's;github.com/gogo/protobuf;gogo protobuf,https://github.com/gogo/protobuf;' \
	-e 's;github.com/pkg/errors;pkg/errors,https://github.com/pkg/errors;' \
	-e 's;github.com/vgough/grpc-proxy;grpc-proxy,https://github.com/vgough/grpc-proxy;' \
	-e 's;golang.org/x/.*;Go,https://github.com/golang/go,9051;' \
	-e 's;k8s.io/.*\|github.com/kubernetes-csi/.*;kubernetes,https://github.com/kubernetes/kubernetes,9641;' \
	-e 's;gopkg.in/fsnotify.*;golang-github-fsnotify-fsnotify,https://github.com/fsnotify/fsnotify;' \
	-e 's;github.com/docker/go-units;go-units,https://github.com/docker/go-units,9173;' \
	| cat |

# Ignore duplicates.
RUNTIME_DEPS += LC_ALL=C LANG=C sort -u

# Execute simple unit tests.
#
# pmem-device-manager gets excluded because its tests need a special
# environment. We could run it, but its tests would just be skipped
# (https://github.com/intel/pmem-csi/pull/420#discussion_r346850741).
.PHONY: run_tests
test: run_tests
RUN_TESTS = TEST_WORK=$(abspath _work) \
	$(TEST_CMD) $(filter-out %/pmem-device-manager,$(TEST_PKGS))
RUN_TEST_DEPS = _work/pmem-ca/.ca-stamp _work/evil-ca/.ca-stamp check-go-version-$(GO_BINARY)

run_tests: $(RUN_TEST_DEPS)
	$(RUN_TESTS)

# E2E tests which are known to be unsuitable (space separated list of regular expressions).
TEST_E2E_SKIP = no-such-test

# The test's check whether a driver supports multiple nodes is incomplete and does
# not work for the topology-based single-node access in PMEM-CSI:
# https://github.com/kubernetes/kubernetes/blob/25ffbe633810609743944edd42d164cd7990071c/test/e2e/storage/testsuites/provisioning.go#L175-L181
TEST_E2E_SKIP += should.access.volume.from.different.nodes

# E2E tests which are to be executed (space separated list of regular expressions, default is all that aren't skipped).
TEST_E2E_FOCUS =

# E2E Junit output directory (default empty = none). junit_<ginkgo node>.xml files will be written there,
# i.e. usually just junit_01.xml.
TEST_E2E_REPORT_DIR=

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
	  echo TEST_DEPLOYMENTMODE=$$TEST_DEPLOYMENTMODE; \
	  echo TEST_DEVICEMODE=$$TEST_DEVICEMODE; \
	  echo TEST_KUBERNETES_VERSION=$$TEST_KUBERNETES_VERSION; \
	  echo PMEM_CSI_IMAGE=$$TEST_LOCAL_REGISTRY/pmem-csi-driver$$(if [ $$TEST_DEPLOYMENTMODE = testing ]; then echo -test; fi):$(IMAGE_VERSION); \
	) \
	TEST_CMD='$(TEST_CMD)' \
	GO='$(GO)' \
	TEST_PKGS='$(shell for i in $(TEST_PKGS); do if ls $$i/*_test.go 2>/dev/null >&2; then echo $$i; fi; done)' \
	$(GO) test -count=1 -timeout 0 -v ./test/e2e \
                -ginkgo.skip='$(subst $(space),|,$(TEST_E2E_SKIP))' \
                -ginkgo.focus='$(subst $(space),|,$(TEST_E2E_FOCUS))' \
                -report-dir=$(TEST_E2E_REPORT_DIR)
test_e2e: start $(RUN_TEST_DEPS)
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
	WORKDIR='$(@D)' PATH='$(PWD)/_work/bin/:$(PATH)' CA='$*' EXTRA_CNS="wrong-node-controller" $<
	touch $@

_work/.setupcfssl-stamp:
	rm -rf _work/bin
	curl -L https://pkg.cfssl.org/R1.2/cfssl_linux-amd64 -o _work/bin/cfssl --create-dirs
	curl -L https://pkg.cfssl.org/R1.2/cfssljson_linux-amd64 -o _work/bin/cfssljson --create-dirs
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
