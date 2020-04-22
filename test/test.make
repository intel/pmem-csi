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

# List direct imports of our commands, ignoring the go standard runtime packages.
RUNTIME_DEPS += diff <(env "GO=$(GO)" hack/list-direct-imports.sh $(IMPORT_PATH) ./cmd/... | grep -v ^github.com/intel/pmem-csi | sort -u) \
                <(go list std | sort -u) | grep ^'<' | cut -f2- -d' ' |


# Filter out some packages that aren't really code.
RUNTIME_DEPS += grep -v -e '^C$$' |
RUNTIME_DEPS += grep -v -e '^github.com/container-storage-interface/spec/lib/go/csi$$' |
RUNTIME_DEPS += grep -v -e '^google.golang.org/genproto/googleapis/rpc/status$$' |
RUNTIME_DEPS += grep -v -e '^github.com/go-logr/logr$$' |

# Reduce the package import paths to project names + download URL.
# - strip prefix
RUNTIME_DEPS += sed -e 's;github.com/intel/pmem-csi/vendor/;;' |
# - use path inside github.com as project name
RUNTIME_DEPS += sed -e 's;^github.com/\([^/]*\)/\([^/]*\).*;github.com/\1/\2;' |
# - same for sigs.k8s.io
RUNTIME_DEPS += sed -e 's;^sigs.k8s.io/\([^/]*\).*;sigs.k8s.io/\1;' |
# - everything from gRPC is one project
RUNTIME_DEPS += sed -e 's;google.golang.org/grpc/*.*;grpc-go,https://github.com/grpc/grpc-go;' |
# - Kubernetes is split across several repos.
RUNTIME_DEPS += sed -e 's;^k8s.io/.*\|github.com/kubernetes-csi/.*;kubernetes,https://github.com/kubernetes/kubernetes,9641;' | \
# - additional Golang repos
RUNTIME_DEPS += sed -e 's;\(golang.org/x/.*\);Go,https://github.com/golang/go,9051;' | \
# - various other projects (sorted alphabetically)
RUNTIME_DEPS += sed \
	-e 's;\(github.com/PuerkitoBio/purell\);purell,https://\1;' \
	-e 's;\(github.com/PuerkitoBio/urlesc\);urlesc,https://\1;' \
	-e 's;\(github.com/beorn7/perks\);perks,https://\1;' \
	-e 's;\(github.com/cespare/xxhash\);go-xxhash,https://\1;' \
	-e 's;\(github.com/coreos/prometheus-operator\);Prometheus Operator,https://\1;' \
	-e 's;\(github.com/davecgh/go-spew\);davecgh/go-spew,https://\1;' \
	-e 's;\(github.com/docker/go-units\);go-units,https://\1,9173;' \
	-e 's;\(github.com/emicklei/go-restful\);go-restful,http://\1,10372;' \
	-e 's;\(github.com/evanphx/json-patch\);json-patch,https://\1;' \
	-e 's;\(github.com/go-openapi/jsonpointer\);gojsonpointer,https://\1;' \
	-e 's;\(github.com/go-openapi/jsonreference\);go-openapi jsonreference,https://\1;' \
	-e 's;\(github.com/go-openapi/spec\);go-openapi spec,https://\1;' \
	-e 's;\(github.com/go-openapi/swag\);go-openapi/swag,https://\1;' \
	-e 's;\(github.com/gogo/protobuf\);gogo protobuf,https://\1;' \
	-e 's;\(github.com/golang/groupcache\);golang-groupcache,https://\1;' \
	-e 's;\(github.com/golang/protobuf\);golang-protobuf,https://\1;' \
	-e 's;\(github.com/google/go-cmp\);go-cmp,https://\1;' \
	-e 's;\(github.com/google/gofuzz\);Google gofuzz,https://\1;' \
	-e 's;\(github.com/google/uuid\);google uuid,https://\1;' \
	-e 's;\(github.com/googleapis/gnostic\);gnostic,https://\1;' \
	-e 's;\(github.com/hashicorp/golang-lru\);golang-lru,https://\1;' \
	-e 's;\(github.com/imdario/mergo\);mergo,https://\1;' \
	-e 's;\(github.com/json-iterator/go\);json-iterator,https://\1;' \
	-e 's;\(github.com/mailru/easyjson\);golang easyjson,https://\1;' \
	-e 's;\(github.com/matttproud/golang_protobuf_extensions\);golang_protobuf_extensions,https://\1;' \
	-e 's;\(github.com/modern-go/concurrent\);concurrent,https://\1;' \
	-e 's;\(github.com/modern-go/reflect2\);reflect2,https://\1;' \
	-e 's;\(github.com/operator-framework/operator-sdk\);operator-sdk,https://\1;' \
	-e 's;\(github.com/pkg/errors\);pkg/errors,https://\1;' \
	-e 's;\(github.com/prometheus/client_golang\);client_golang,https://\1;' \
	-e 's;\(github.com/prometheus/client_model\);prometheus client_model,https://\1;' \
	-e 's;\(github.com/prometheus/common\);prometheus_common,https://\1;' \
	-e 's;\(github.com/prometheus/procfs\);prometheus_procfs,https://\1;' \
	-e 's;\(github.com/spf13/pflag\);github.com\\spf13\\pflag,https://\1;' \
	-e 's;\(github.com/vgough/grpc-proxy\);grpc-proxy,https://\1;' \
	-e 's;gomodules.xyz/jsonpatch/v.*;gomodules jsonpatch,https://github.com/gomodules/jsonpatch;' \
	-e 's;gopkg.in/fsnotify.*;golang-github-fsnotify-fsnotify,https://github.com/fsnotify/fsnotify;' \
	-e 's;gopkg.in/inf\.v.*;go-inf,https://github.com/go-inf/inf;' \
	-e 's;gopkg.in/yaml\.v.*;go-yaml,https://https://github.com/go-yaml/yaml,9476;' \
	-e 's;sigs.k8s.io/controller-runtime;kubernetes-sigs/controller-runtime,https://github.com/kubernetes-sigs/controller-runtime;' \
	-e 's;sigs.k8s.io/yaml;kubernetes-sigs/yaml,https://github.com/kubernetes-sigs/yaml;' \
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

# E2E tests which are known to be unsuitable (space or @ separated list of regular expressions).
TEST_E2E_SKIP =
TEST_E2E_SKIP_ALL = $(TEST_E2E_SKIP)

# The test's check whether a driver supports multiple nodes is incomplete and does
# not work for the topology-based single-node access in PMEM-CSI:
# https://github.com/kubernetes/kubernetes/blob/25ffbe633810609743944edd42d164cd7990071c/test/e2e/storage/testsuites/provisioning.go#L175-L181
TEST_E2E_SKIP_ALL += should.access.volume.from.different.nodes

# This is a test for behavior of kubelet which Kubernetes <= 1.15 doesn't pass.
TEST_E2E_SKIP_1.14 += volumeMode.should.not.mount.*map.unused.volumes.in.a.pod
TEST_E2E_SKIP_1.15 += volumeMode.should.not.mount.*map.unused.volumes.in.a.pod

# It looks like Kubernetes <= 1.15 does not wait for
# NodeUnpublishVolume to complete before deleting the pod:
#
# Apr 21 17:33:12.743: INFO: Wait up to 5m0s for pod "dax-volume-test" to be fully deleted
# pmem-csi-node-4dsmr/pmem-driver@pmem..ker2: I0421 17:33:34.491659       1 tracing.go:19] GRPC call: /csi.v1.Node/NodeGetCapabilities
# pmem-csi-node-4dsmr/pmem-driver@pmem..ker2: I0421 17:33:45.549013       1 tracing.go:19] GRPC call: /csi.v1.Node/NodeUnpublishVolume
# pmem-csi-node-4dsmr/pmem-driver@pmem..ker2: I0421 17:33:45.549189       1 nodeserver.go:295] NodeUnpublishVolume: unmount /var/lib/kubelet/pods/1c5f1fec-b08b-4264-8c55-40a22c1b3d16/volumes/kubernetes.io~csi/vol1/mount
# STEP: delete the pod
# Apr 21 17:33:46.769: INFO: Waiting for pod dax-volume-test to disappear
# Apr 21 17:33:46.775: INFO: Pod dax-volume-test no longer exists
#
# That breaks our volume leak detection because the test continues
# before the volume is truly removed. As a workaround, we disable
# ephemeral volume tests on Kubernetes <= 1.15. That's okay because the feature
# was alpha in those releases and shouldn't be used.
TEST_E2E_SKIP_1.14 += Testpattern:.Ephemeral-volume Testpattern:.inline.ephemeral.CSI.volume
TEST_E2E_SKIP_1.15 += Testpattern:.Ephemeral-volume Testpattern:.inline.ephemeral.CSI.volume

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
	TEST_PKGS='$(shell for i in $(TEST_PKGS); do if ls $$i/*_test.go 2>/dev/null >&2; then echo $$i; fi; done)' \
	$(GO) test -count=1 -timeout 0 -v ./test/e2e \
                -ginkgo.skip='$(subst $(space),|,$(strip $(subst @,$(space),$(TEST_E2E_SKIP_ALL))))' \
                -ginkgo.focus='$(subst $(space),|,$(strip $(subst @,$(space),$(TEST_E2E_FOCUS))))' \
		-ginkgo.randomizeAllSpecs=false \
	        $(TEST_E2E_ARGS) \
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
