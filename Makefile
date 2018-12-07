# Copyright 2017 The Kubernetes Authors.
# Copyright 2018 Intel Corporation
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

IMPORT_PATH=github.com/intel/pmem-csi
SHELL=bash

REGISTRY_NAME=localhost:5000
IMAGE_VERSION_pmem-csi-driver=canary
IMAGE_VERSION_pmem-ns-init=canary
IMAGE_VERSION_pmem-vgm=canary
IMAGE_TAG=$(REGISTRY_NAME)/$*:$(IMAGE_VERSION_$*)
IMAGE_BUILD_ARGS=
# Pass proxy config via --build-arg only if these are set,
# enabling proxy config other way, like ~/.docker/config.json
BUILD_ARGS=
ifneq ($(http_proxy),)
	BUILD_ARGS:=${BUILD_ARGS} --build-arg http_proxy=${http_proxy}
endif
ifneq ($(https_proxy),)
	BUILD_ARGS:=${BUILD_ARGS} --build-arg https_proxy=${https_proxy}
endif
ifneq ($(no_proxy),)
	BUILD_ARGS:=${BUILD_ARGS} --build-arg no_proxy=${no_proxy}
endif

all: pmem-csi-driver pmem-ns-init pmem-vgm

build-images: build-pmem-csi-driver-image build-pmem-ns-init-image build-pmem-vgm-image

push-images: push-pmem-csi-driver-image push-pmem-ns-init-image push-pmem-vgm-image

test:
	go test $(IMPORT_PATH)/pkg/... -cover
	go vet $(IMPORT_PATH)/pkg/...

pmem-csi-driver pmem-vgm pmem-ns-init:
	GOOS=linux go build -a -o _output/$@ ./cmd/$@

build-%-image:
	docker build ${BUILD_ARGS} ${IMAGE_BUILD_ARGS} -t $(IMAGE_TAG) -f ./cmd/$*/Dockerfile .

push-%-image: build-%-image
	docker push $(IMAGE_TAG)

clean:
	go clean -r -x
	-rm -rf _output

# Check resp. fix formatting.
.PHONY: test_fmt fmt
test: test_fmt
test_fmt:
	@ files=$$(find pkg cmd -name '*.go'); \
	if [ $$(gofmt -d $$files | wc -l) -ne 0 ]; then \
		echo "formatting errors:"; \
		gofmt -d $$files; \
		false; \
	fi
fmt:
	gofmt -l -w $$(find pkg cmd -name '*.go')

.PHONY: all test clean pmem-csi-driver pmem-ns-init pmem-vgm

# Add support for creating and booting a cluster under QEMU.
include test/clear-kvm.make
include test/start-stop.make

# This ensures that the vendor directory and vendor-bom.csv are in sync
# at least as far as the listed components go.
.PHONY: test_vendor_bom
test: test_vendor_bom
test_vendor_bom:
	@ if ! diff -c \
		<(tail +2 vendor-bom.csv | sed -e 's/;.*//') \
		<((grep '^  name =' Gopkg.lock  | sed -e 's/.*"\(.*\)"/\1/') | sort); then \
		echo; \
		echo "vendor-bom.csv not in sync with vendor directory (aka Gopk.lock):"; \
		echo "+ new entry, missing in vendor-bom.csv"; \
		echo "- obsolete entry in vendor-bom.csv"; \
		false; \
	fi

# This ensures that we know about all components that are needed at
# runtime on a production system. Those must be scrutinized more
# closely than components that are merely needed for testing.
#
# Intel has a process for this. The mapping from import path to "name"
# + "download URL" must match how the components are identified at
# Intel while reviewing the components.
.PHONY: test_runtime_deps
test: test_runtime_deps
test_runtime_deps:
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
RUNTIME_DEPS += go list -f '{{ join .Deps "\n" }}' ./cmd/pmem-csi-driver |

# This focuses on packages that are not in Golang core.
RUNTIME_DEPS += grep '^github.com/intel/pmem-csi/vendor/' |

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
	-e 's;github.com/golang/glog;glog,https://github.com/golang/glog;' \
	-e 's;github.com/pkg/errors;pkg/errors,https://github.com/pkg/errors;' \
	-e 's;github.com/vgough/grpc-proxy;grpc-proxy,https://github.com/vgough/grpc-proxy;' \
	-e 's;golang.org/x/.*;Go,https://github.com/golang/go;' \
	-e 's;k8s.io/.*;kubernetes,https://github.com/kubernetes/kubernetes;' \
	-e 's;gopkg.in/fsnotify.*;golang-github-fsnotify-fsnotify,https://github.com/fsnotify/fsnotify;' \
	| cat |

# Ignore duplicates.
RUNTIME_DEPS += sort -u

# E2E testing relies on a running QEMU test cluster. It therefore starts it,
# but because it might have been running already and might have to be kept
# running to debug test failures, it doesn't stop it
.PHONY: test_e2e
test_e2e: start
	KUBECONFIG=`pwd`/_work/clear-kvm-kube.config REPO_ROOT=`pwd` go test -timeout 0 ./test/e2e
